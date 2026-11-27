//! Dynamic User Attributes & Per-Tenant Attribute Schema Registry (WP2.7).
//!
//! Stores normalized `(tenant_id, user_id, attribute_key, value_text)` rows in
//! `user_attribute` with composite B-tree indexes for large-scale queries, while
//! keeping `user_account.attributes` (JSONB), `attributes_version` (CAS), and
//! `user_attribute_history` (audit) synchronized in the same transaction.

use crate::{
    error::ApiError,
    middleware::service_auth::ServicePrincipal,
    state::AppState,
};
use authz_core::AuthzError;
use axum::{
    extract::{Extension, Path, Query, State},
    http::StatusCode,
    Json,
};
use serde::{Deserialize, Serialize};
use serde_json::{Map as JsonMap, Value as JsonValue};
use sqlx::Row;
use std::collections::{BTreeMap, BTreeSet};
use uuid::Uuid;

pub const MAX_ATTRIBUTE_KEY_LEN: usize = 64;
pub const MAX_ATTRIBUTE_VALUE_LEN: usize = 128;
pub const MAX_VALUES_PER_ATTRIBUTE: usize = 32;
pub const MAX_ATTRIBUTES_PER_USER: usize = 32;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AttributeDefinitionResponse {
    pub id: Uuid,
    pub tenant_id: Uuid,
    pub attribute_key: String,
    pub value_type: String,
    pub max_values: i32,
    pub workflow_namespaces: Vec<String>,
    pub description: String,
    pub is_active: bool,
    pub version: i64,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct UpsertAttributeDefinitionRequest {
    pub tenant_id: Uuid,
    pub attribute_key: String,
    pub value_type: String,
    #[serde(default = "default_max_values")]
    pub max_values: i32,
    #[serde(default)]
    pub workflow_namespaces: Vec<String>,
    #[serde(default)]
    pub description: String,
    #[serde(default = "default_true")]
    pub is_active: bool,
}

fn default_max_values() -> i32 {
    1
}

fn default_true() -> bool {
    true
}

#[derive(Debug, Deserialize)]
pub struct TenantScopedQuery {
    pub tenant_id: Uuid,
}

#[derive(Debug, Serialize)]
pub struct UserAttributesResponse {
    pub tenant_id: Uuid,
    pub user_id: Uuid,
    pub attributes_version: i64,
    pub attributes: BTreeMap<String, Vec<String>>,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReplaceUserAttributesRequest {
    pub tenant_id: Uuid,
    pub expected_attributes_version: i64,
    pub attributes: BTreeMap<String, Vec<String>>,
}

#[derive(Debug, Deserialize)]
pub struct LookupUsersByAttributeQuery {
    pub tenant_id: Uuid,
    pub key: String,
    pub value: Option<String>,
    pub min_int: Option<i64>,
    pub max_int: Option<i64>,
    #[serde(default = "default_lookup_limit")]
    pub limit: i64,
}

fn default_lookup_limit() -> i64 {
    100
}

#[derive(Debug, Serialize)]
pub struct LookupUsersByAttributeResponse {
    pub tenant_id: Uuid,
    pub key: String,
    pub user_ids: Vec<Uuid>,
}

#[derive(Debug, Clone)]
pub struct ParsedAttributeValue {
    pub value_text: String,
    pub value_int: Option<i64>,
    pub value_bool: Option<bool>,
}

fn require_tenant_cap(
    principal: &ServicePrincipal,
    tenant_id: Uuid,
    capability: &str,
) -> Result<(), ApiError> {
    if principal.tenant_id.as_deref() != Some(tenant_id.to_string().as_str())
        || !principal.capabilities.iter().any(|c| c == capability)
    {
        return Err(AuthzError::PolicyDenied {
            policy_name: capability.into(),
            reason: format!("principal lacks {capability} for tenant {tenant_id}"),
        }
        .into());
    }
    Ok(())
}

pub fn validate_attribute_key(key: &str) -> Result<(), ApiError> {
    let bytes = key.as_bytes();
    if bytes.is_empty() || bytes.len() > MAX_ATTRIBUTE_KEY_LEN {
        return Err(invalid_request("attribute_key must be 1..=64 ASCII characters"));
    }
    if !bytes[0].is_ascii_lowercase() {
        return Err(invalid_request("attribute_key must start with a lowercase ASCII letter"));
    }
    if !bytes
        .iter()
        .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || matches!(b, b'.' | b'_' | b'-'))
    {
        return Err(invalid_request(
            "attribute_key may only contain lowercase letters, digits, '.', '_', or '-'",
        ));
    }
    Ok(())
}

pub fn validate_value_type_and_limit(value_type: &str, max_values: i32) -> Result<(), ApiError> {
    if !matches!(value_type, "string" | "string_list" | "int64" | "bool") {
        return Err(invalid_request(
            "value_type must be one of: string, string_list, int64, bool",
        ));
    }
    if max_values < 1 || max_values > MAX_VALUES_PER_ATTRIBUTE as i32 {
        return Err(invalid_request("max_values must be between 1 and 32"));
    }
    if value_type != "string_list" && max_values != 1 {
        return Err(invalid_request(
            "max_values must be 1 for scalar types (string, int64, bool)",
        ));
    }
    Ok(())
}

pub fn parse_and_validate_values(
    key: &str,
    value_type: &str,
    max_values: i32,
    raw_values: &[String],
) -> Result<Vec<ParsedAttributeValue>, ApiError> {
    if raw_values.is_empty() {
        return Err(invalid_request(&format!(
            "attribute '{key}' must contain at least one value"
        )));
    }
    let mut unique = BTreeSet::new();
    let mut parsed = Vec::with_capacity(raw_values.len());
    for raw in raw_values {
        let trimmed = raw.trim();
        if trimmed.is_empty() || trimmed.len() > MAX_ATTRIBUTE_VALUE_LEN {
            return Err(invalid_request(&format!(
                "attribute '{key}' values must be 1..={MAX_ATTRIBUTE_VALUE_LEN} bytes"
            )));
        }
        let item = match value_type {
            "string" | "string_list" => ParsedAttributeValue {
                value_text: trimmed.to_owned(),
                value_int: None,
                value_bool: None,
            },
            "int64" => {
                let n = trimmed.parse::<i64>().map_err(|_| {
                    invalid_request(&format!("attribute '{key}' expects int64 value"))
                })?;
                ParsedAttributeValue {
                    value_text: n.to_string(),
                    value_int: Some(n),
                    value_bool: None,
                }
            }
            "bool" => {
                let b = match trimmed {
                    "true" => true,
                    "false" => false,
                    _ => {
                        return Err(invalid_request(&format!(
                            "attribute '{key}' expects boolean 'true' or 'false'"
                        )));
                    }
                };
                ParsedAttributeValue {
                    value_text: if b { "true".into() } else { "false".into() },
                    value_int: None,
                    value_bool: Some(b),
                }
            }
            _ => return Err(invalid_request("unsupported attribute value_type")),
        };
        if unique.insert(item.value_text.clone()) {
            parsed.push(item);
        }
    }
    if parsed.len() > max_values as usize {
        return Err(invalid_request(&format!(
            "attribute '{key}' exceeds max_values ({max_values})"
        )));
    }
    parsed.sort_unstable_by(|a, b| a.value_text.cmp(&b.value_text));
    Ok(parsed)
}

pub async fn list_attribute_definitions(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<TenantScopedQuery>,
) -> Result<Json<Vec<AttributeDefinitionResponse>>, ApiError> {
    require_tenant_cap(&principal, query.tenant_id, "identity.attributes.schema.read")?;
    let rows = sqlx::query(
        r#"SELECT id, tenant_id, attribute_key, value_type, max_values,
                  workflow_namespaces, description, is_active, version
           FROM tenant_attribute_definition
           WHERE tenant_id = $1
           ORDER BY attribute_key"#,
    )
    .bind(query.tenant_id)
    .fetch_all(&state.pool)
    .await?;

    let items = rows
        .into_iter()
        .map(|row| {
            Ok(AttributeDefinitionResponse {
                id: row.try_get("id")?,
                tenant_id: row.try_get("tenant_id")?,
                attribute_key: row.try_get("attribute_key")?,
                value_type: row.try_get("value_type")?,
                max_values: row.try_get("max_values")?,
                workflow_namespaces: row.try_get("workflow_namespaces")?,
                description: row.try_get("description")?,
                is_active: row.try_get("is_active")?,
                version: row.try_get("version")?,
            })
        })
        .collect::<Result<Vec<_>, sqlx::Error>>()?;
    Ok(Json(items))
}

pub async fn upsert_attribute_definition(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Json(req): Json<UpsertAttributeDefinitionRequest>,
) -> Result<(StatusCode, Json<AttributeDefinitionResponse>), ApiError> {
    require_tenant_cap(&principal, req.tenant_id, "identity.attributes.schema.manage")?;
    validate_attribute_key(&req.attribute_key)?;
    validate_value_type_and_limit(&req.value_type, req.max_values)?;
    let actor_uuid = principal
        .actor_id
        .as_deref()
        .and_then(|s| Uuid::parse_str(s).ok());

    let row = sqlx::query(
        r#"INSERT INTO tenant_attribute_definition
             (tenant_id, attribute_key, value_type, max_values, workflow_namespaces,
              description, is_active, created_by, updated_by)
           VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
           ON CONFLICT (tenant_id, attribute_key)
           DO UPDATE SET
             value_type = EXCLUDED.value_type,
             max_values = EXCLUDED.max_values,
             workflow_namespaces = EXCLUDED.workflow_namespaces,
             description = EXCLUDED.description,
             is_active = EXCLUDED.is_active,
             version = tenant_attribute_definition.version + 1,
             updated_by = EXCLUDED.updated_by
           RETURNING id, tenant_id, attribute_key, value_type, max_values,
                     workflow_namespaces, description, is_active, version"#,
    )
    .bind(req.tenant_id)
    .bind(&req.attribute_key)
    .bind(&req.value_type)
    .bind(req.max_values)
    .bind(&req.workflow_namespaces)
    .bind(&req.description)
    .bind(req.is_active)
    .bind(actor_uuid)
    .fetch_one(&state.pool)
    .await?;

    Ok((
        StatusCode::OK,
        Json(AttributeDefinitionResponse {
            id: row.try_get("id")?,
            tenant_id: row.try_get("tenant_id")?,
            attribute_key: row.try_get("attribute_key")?,
            value_type: row.try_get("value_type")?,
            max_values: row.try_get("max_values")?,
            workflow_namespaces: row.try_get("workflow_namespaces")?,
            description: row.try_get("description")?,
            is_active: row.try_get("is_active")?,
            version: row.try_get("version")?,
        }),
    ))
}

pub async fn get_user_attributes(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Path(user_id): Path<Uuid>,
    Query(query): Query<TenantScopedQuery>,
) -> Result<Json<UserAttributesResponse>, ApiError> {
    require_tenant_cap(&principal, query.tenant_id, "identity.attributes.read")?;
    let user_row = sqlx::query(
        "SELECT attributes_version FROM user_account WHERE id = $1 AND tenant_id = $2 AND NOT is_deleted",
    )
    .bind(user_id)
    .bind(query.tenant_id)
    .fetch_optional(&state.pool)
    .await?
    .ok_or(AuthzError::UserNotFound {
        user_id,
        tenant_id: query.tenant_id,
    })?;
    let attributes_version: i64 = user_row.try_get("attributes_version")?;

    let rows = sqlx::query(
        r#"SELECT ua.attribute_key, ua.value_text
           FROM user_attribute ua
           JOIN tenant_attribute_definition tad
             ON tad.tenant_id = ua.tenant_id
            AND tad.attribute_key = ua.attribute_key
            AND tad.is_active
           WHERE ua.tenant_id = $1 AND ua.user_id = $2
           ORDER BY ua.attribute_key, ua.value_text"#,
    )
    .bind(query.tenant_id)
    .bind(user_id)
    .fetch_all(&state.pool)
    .await?;

    let mut attributes: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for row in rows {
        let key: String = row.try_get("attribute_key")?;
        let val: String = row.try_get("value_text")?;
        attributes.entry(key).or_default().push(val);
    }

    Ok(Json(UserAttributesResponse {
        tenant_id: query.tenant_id,
        user_id,
        attributes_version,
        attributes,
    }))
}

pub async fn replace_user_attributes(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Path(user_id): Path<Uuid>,
    Json(req): Json<ReplaceUserAttributesRequest>,
) -> Result<Json<UserAttributesResponse>, ApiError> {
    require_tenant_cap(&principal, req.tenant_id, "identity.attributes.manage")?;
    if req.attributes.len() > MAX_ATTRIBUTES_PER_USER {
        return Err(invalid_request(&format!(
            "a user may have at most {MAX_ATTRIBUTES_PER_USER} attributes"
        )));
    }
    let actor_uuid = principal
        .actor_id
        .as_deref()
        .and_then(|s| Uuid::parse_str(s).ok());

    let mut tx = state.pool.begin().await?;

    // Lock user row and check optimistic concurrency (CAS on attributes_version).
    let user_row = sqlx::query(
        r#"SELECT attributes_version, attributes
           FROM user_account
           WHERE id = $1 AND tenant_id = $2 AND NOT is_deleted
           FOR UPDATE"#,
    )
    .bind(user_id)
    .bind(req.tenant_id)
    .fetch_optional(&mut *tx)
    .await?
    .ok_or(AuthzError::UserNotFound {
        user_id,
        tenant_id: req.tenant_id,
    })?;
    let current_version: i64 = user_row.try_get("attributes_version")?;
    if current_version != req.expected_attributes_version {
        return Err(AuthzError::VersionConflict {
            entity: "user_account",
            entity_id: user_id,
            expected_version: req.expected_attributes_version,
            actual_version: current_version,
        }
        .into());
    }

    // Load active schema definitions for this tenant.
    let def_rows = sqlx::query(
        r#"SELECT attribute_key, value_type, max_values
           FROM tenant_attribute_definition
           WHERE tenant_id = $1 AND is_active"#,
    )
    .bind(req.tenant_id)
    .fetch_all(&mut *tx)
    .await?;
    let mut defs: BTreeMap<String, (String, i32)> = BTreeMap::new();
    for row in def_rows {
        let key: String = row.try_get("attribute_key")?;
        let vtype: String = row.try_get("value_type")?;
        let max_v: i32 = row.try_get("max_values")?;
        defs.insert(key, (vtype, max_v));
    }

    // Validate all submitted keys/values against active schema definitions.
    let mut validated: BTreeMap<String, Vec<ParsedAttributeValue>> = BTreeMap::new();
    let mut json_projection = JsonMap::new();
    for (key, values) in &req.attributes {
        let Some((vtype, max_v)) = defs.get(key) else {
            return Err(invalid_request(&format!(
                "attribute '{key}' is not registered or not active in tenant_attribute_definition"
            )));
        };
        let parsed = parse_and_validate_values(key, vtype, *max_v, values)?;
        let json_val = match vtype.as_str() {
            "string_list" => JsonValue::Array(
                parsed
                    .iter()
                    .map(|v| JsonValue::String(v.value_text.clone()))
                    .collect(),
            ),
            "int64" => JsonValue::from(parsed[0].value_int.unwrap_or(0)),
            "bool" => JsonValue::from(parsed[0].value_bool.unwrap_or(false)),
            _ => JsonValue::String(parsed[0].value_text.clone()),
        };
        json_projection.insert(key.clone(), json_val);
        validated.insert(key.clone(), parsed);
    }

    // Snapshot previous attributes for audit history.
    let prev_rows = sqlx::query(
        "SELECT attribute_key, value_text FROM user_attribute WHERE tenant_id = $1 AND user_id = $2 ORDER BY attribute_key, value_text",
    )
    .bind(req.tenant_id)
    .bind(user_id)
    .fetch_all(&mut *tx)
    .await?;
    let mut prev_map: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for row in prev_rows {
        let k: String = row.try_get("attribute_key")?;
        let v: String = row.try_get("value_text")?;
        prev_map.entry(k).or_default().push(v);
    }

    // Replace normalized rows in user_attribute.
    sqlx::query("DELETE FROM user_attribute WHERE tenant_id = $1 AND user_id = $2")
        .bind(req.tenant_id)
        .bind(user_id)
        .execute(&mut *tx)
        .await?;

    let mut response_attrs: BTreeMap<String, Vec<String>> = BTreeMap::new();
    for (key, items) in &validated {
        let mut texts = Vec::with_capacity(items.len());
        for item in items {
            sqlx::query(
                r#"INSERT INTO user_attribute
                     (tenant_id, user_id, attribute_key, value_text, value_int, value_bool)
                   VALUES ($1, $2, $3, $4, $5, $6)"#,
            )
            .bind(req.tenant_id)
            .bind(user_id)
            .bind(key)
            .bind(&item.value_text)
            .bind(item.value_int)
            .bind(item.value_bool)
            .execute(&mut *tx)
            .await?;
            texts.push(item.value_text.clone());
        }
        response_attrs.insert(key.clone(), texts);
    }

    // Sync user_account.attributes (JSONB) and bump attributes_version atomically.
    let new_version = current_version + 1;
    sqlx::query(
        "UPDATE user_account SET attributes = $1, attributes_version = $2 WHERE id = $3 AND tenant_id = $4",
    )
    .bind(JsonValue::Object(json_projection))
    .bind(new_version)
    .bind(user_id)
    .bind(req.tenant_id)
    .execute(&mut *tx)
    .await?;

    // Write attribute diffs to user_attribute_history.
    let all_keys: BTreeSet<&String> = prev_map.keys().chain(response_attrs.keys()).collect();
    for key in all_keys {
        let old_vals = prev_map.get(key);
        let new_vals = response_attrs.get(key);
        if old_vals != new_vals {
            let old_text = old_vals.map(|v| serde_json::to_string(v).unwrap_or_default());
            let new_text = new_vals.map(|v| serde_json::to_string(v).unwrap_or_default());
            sqlx::query(
                r#"INSERT INTO user_attribute_history
                     (user_id, attribute, old_value, new_value, changed_by)
                   VALUES ($1, $2, $3, $4, $5)"#,
            )
            .bind(user_id)
            .bind(key)
            .bind(old_text)
            .bind(new_text)
            .bind(actor_uuid)
            .execute(&mut *tx)
            .await?;
        }
    }

    tx.commit().await?;

    Ok(Json(UserAttributesResponse {
        tenant_id: req.tenant_id,
        user_id,
        attributes_version: new_version,
        attributes: response_attrs,
    }))
}

/// High-performance indexed lookup of users by attribute value or integer range.
/// Uses `idx_user_attribute_lookup_text` or `idx_user_attribute_lookup_int`.
pub async fn find_users_by_attribute(
    State(state): State<AppState>,
    Extension(principal): Extension<ServicePrincipal>,
    Query(query): Query<LookupUsersByAttributeQuery>,
) -> Result<Json<LookupUsersByAttributeResponse>, ApiError> {
    require_tenant_cap(&principal, query.tenant_id, "identity.attributes.read")?;
    validate_attribute_key(&query.key)?;
    let limit = query.limit.clamp(1, 500);

    let rows = if let Some(value) = query.value.as_deref() {
        sqlx::query(
            r#"SELECT DISTINCT user_id
               FROM user_attribute
               WHERE tenant_id = $1 AND attribute_key = $2 AND value_text = $3
               ORDER BY user_id
               LIMIT $4"#,
        )
        .bind(query.tenant_id)
        .bind(&query.key)
        .bind(value)
        .bind(limit)
        .fetch_all(&state.pool)
        .await?
    } else if query.min_int.is_some() || query.max_int.is_some() {
        sqlx::query(
            r#"SELECT DISTINCT user_id
               FROM user_attribute
               WHERE tenant_id = $1
                 AND attribute_key = $2
                 AND value_int IS NOT NULL
                 AND ($3::bigint IS NULL OR value_int >= $3)
                 AND ($4::bigint IS NULL OR value_int <= $4)
               ORDER BY user_id
               LIMIT $5"#,
        )
        .bind(query.tenant_id)
        .bind(&query.key)
        .bind(query.min_int)
        .bind(query.max_int)
        .bind(limit)
        .fetch_all(&state.pool)
        .await?
    } else {
        return Err(invalid_request(
            "provide either 'value' or numeric bounds ('min_int' / 'max_int')",
        ));
    };

    let user_ids = rows
        .into_iter()
        .map(|row| row.try_get("user_id"))
        .collect::<Result<Vec<Uuid>, sqlx::Error>>()?;

    Ok(Json(LookupUsersByAttributeResponse {
        tenant_id: query.tenant_id,
        key: query.key,
        user_ids,
    }))
}

fn invalid_request(reason: &str) -> ApiError {
    AuthzError::InvalidRequest {
        reason: reason.to_owned(),
    }
    .into()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn validates_attribute_keys() {
        assert!(validate_attribute_key("region").is_ok());
        assert!(validate_attribute_key("loan.approval_limit").is_ok());
        assert!(validate_attribute_key("credit-tier_1").is_ok());
        assert!(validate_attribute_key("").is_err());
        assert!(validate_attribute_key("Region").is_err());
        assert!(validate_attribute_key("1invalid").is_err());
        assert!(validate_attribute_key("bad space").is_err());
    }

    #[test]
    fn parses_and_validates_typed_values() {
        let ints = parse_and_validate_values("loan.approval_limit", "int64", 1, &["50000000".into()])
            .ok()
            .expect("valid int64");
        assert_eq!(ints.len(), 1);
        assert_eq!(ints[0].value_int, Some(50_000_000));

        assert!(parse_and_validate_values("loan.approval_limit", "int64", 1, &["abc".into()]).is_err());

        let list = parse_and_validate_values(
            "regions",
            "string_list",
            3,
            &["SOUTH".into(), "NORTH".into(), "NORTH".into()],
        )
        .ok()
        .expect("valid string_list");
        assert_eq!(
            list.iter().map(|v| v.value_text.as_str()).collect::<Vec<_>>(),
            vec!["NORTH", "SOUTH"]
        );

        let flag = parse_and_validate_values("vip_access", "bool", 1, &["true".into()])
            .ok()
            .expect("valid bool");
        assert_eq!(flag[0].value_bool, Some(true));
        assert!(parse_and_validate_values("vip_access", "bool", 1, &["yes".into()]).is_err());
    }
}
