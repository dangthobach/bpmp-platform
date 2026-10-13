//! Typed query PDP: actor proof is independent of authenticated workload identity.
use crate::{
    error::ApiError,
    middleware::service_auth::{verify_jwt, ServicePrincipal},
    state::AppState,
};
use authz_core::{
    ids::{TenantId, UserId},
    models::{
        filter::FilterBackend,
        policy::{AuthzDecision, ComparisonOperator, ConditionNode, ValueSource},
    },
    AuthzError,
};
use authz_engine::{
    context::{AuthzContext, EnvContext, ResourceContext},
    evaluator::pipeline::AuthzRequest,
};
use axum::{extract::State, Extension, Json};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sqlx::Row;
use uuid::Uuid;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct QueryRequest {
    pub tenant_id: Uuid,
    pub actor_jwt: String,
    pub resource_type: String,
    pub action: String,
}
#[derive(Serialize)]
pub struct QueryDecision {
    pub schema_version: u32,
    pub tenant_id: Uuid,
    pub actor_id: Uuid,
    pub decision: AuthzDecision,
    pub policy_version: String,
    pub revoke_epoch: u64,
    pub revoke_sequence: i64,
    pub all_rows: bool,
    pub row_filter: Option<ConditionNode>,
    pub allowed_fields: Vec<String>,
    pub masked_fields: Vec<String>,
}
fn invalid(message: &str) -> AuthzError {
    AuthzError::InvalidRequest {
        reason: message.into(),
    }
}
pub async fn query(
    State(state): State<AppState>,
    Extension(workload): Extension<ServicePrincipal>,
    Json(body): Json<QueryRequest>,
) -> Result<Json<QueryDecision>, ApiError> {
    if workload.tenant_id.as_deref() != Some(body.tenant_id.to_string().as_str())
        || !workload.capabilities.iter().any(|c| c == "authz.filter")
        || body.resource_type.is_empty()
        || body.action.is_empty()
    {
        return Err(invalid("query workload or resource invalid").into());
    }
    let actor = verify_jwt(&body.actor_jwt, &state.jwt_jwks_url, &state.jwt_audience)
        .await
        .map_err(|_| invalid("invalid actor proof"))?;
    if actor.tenant_id.as_deref() != Some(body.tenant_id.to_string().as_str())
        || actor
            .issued_at
            .is_none_or(|iat| iat > chrono::Utc::now().timestamp())
    {
        return Err(invalid("invalid actor tenant or issued-at").into());
    }
    let user_id = Uuid::parse_str(&actor.subject)
        .map_err(|_| invalid("actor subject must be registered user UUID"))?;
    let tenant = TenantId::from_uuid(body.tenant_id);
    let before: i64 =
        sqlx::query_scalar("SELECT revision FROM query_security_revision WHERE singleton")
            .fetch_one(&state.pool)
            .await
            .map_err(AuthzError::from)?;
    let user = authz_db::find_user_by_id(&state.pool, tenant, UserId::from_uuid(user_id)).await?;
    let revoke = sqlx::query("SELECT query_revoke_epoch,query_revoked FROM user_account WHERE tenant_id=$1 AND id=$2 AND NOT is_deleted")
        .bind(body.tenant_id).bind(user_id).fetch_one(&state.pool).await.map_err(AuthzError::from)?;
    let epoch: i64 = revoke.get("query_revoke_epoch");
    let revoked: bool = revoke.get("query_revoked");
    let ctx = AuthzContext {
        tenant_id: tenant,
        user_id: UserId::from_uuid(user_id),
        user_attributes: user.attributes,
        user_attributes_version: user.attributes_version,
        resource: ResourceContext {
            resource_type: body.resource_type,
            resource_ref: None,
            attributes: Value::Object(Default::default()),
        },
        env: EnvContext::default(),
        backend: FilterBackend::Sql,
    };
    let response = state
        .pipeline
        .evaluate_query(&AuthzRequest {
            tenant_id: tenant,
            user_id: UserId::from_uuid(user_id),
            action: body.action,
            context: ctx.clone(),
            include_trace: false,
        })
        .await?;
    let filter = match response.row_filter_ast {
        Some(ast) => Some(resolve_live(ast, &ctx, &state, 0).await?),
        None => None,
    };
    let after: i64 =
        sqlx::query_scalar("SELECT revision FROM query_security_revision WHERE singleton")
            .fetch_one(&state.pool)
            .await
            .map_err(AuthzError::from)?;
    // A policy/relation/attribute/revoke write during evaluation invalidates the decision.
    let decision =
        if before != after || revoked || actor.revoke_epoch.is_none_or(|e| e < epoch as u64) {
            AuthzDecision::Deny
        } else {
            response.decision
        };
    let fields = response.field_filter;
    Ok(Json(QueryDecision {
        schema_version: 1,
        tenant_id: body.tenant_id,
        actor_id: user_id,
        decision,
        policy_version: format!("query-{after}"),
        revoke_epoch: epoch as u64,
        revoke_sequence: after,
        all_rows: filter.is_none() && decision == AuthzDecision::Allow,
        row_filter: filter,
        allowed_fields: fields
            .as_ref()
            .map_or_else(Vec::new, |f| f.allowed_fields.clone()),
        masked_fields: fields.map_or_else(Vec::new, |f| f.masked_fields),
    }))
}

fn resolve_value(value: ValueSource, ctx: &AuthzContext) -> Result<ValueSource, AuthzError> {
    Ok(match value {
        ValueSource::UserAttr { key } => ValueSource::Literal {
            value: ctx
                .user_attr(&key)
                .filter(|v| !v.is_null())
                .ok_or_else(|| invalid("missing row policy attribute"))?
                .clone(),
        },
        ValueSource::Literal { .. } | ValueSource::ResourceField { .. } => value,
        _ => return Err(invalid("unsupported query policy value source")),
    })
}

fn resolve_live<'a>(
    node: ConditionNode,
    ctx: &'a AuthzContext,
    state: &'a AppState,
    depth: usize,
) -> std::pin::Pin<
    Box<dyn std::future::Future<Output = Result<ConditionNode, AuthzError>> + Send + 'a>,
> {
    Box::pin(async move {
        if depth > 32 {
            return Err(invalid("row policy depth exceeded"));
        }
        match node {
            ConditionNode::And { conditions } => {
                let mut out = Vec::with_capacity(conditions.len());
                for n in conditions {
                    out.push(resolve_live(n, ctx, state, depth + 1).await?);
                }
                Ok(ConditionNode::And { conditions: out })
            }
            ConditionNode::Or { conditions } => {
                let mut out = Vec::with_capacity(conditions.len());
                for n in conditions {
                    out.push(resolve_live(n, ctx, state, depth + 1).await?);
                }
                Ok(ConditionNode::Or { conditions: out })
            }
            ConditionNode::Leaf(mut leaf) => {
                if let ValueSource::Relation { key, target } = &leaf.left {
                    if leaf.op != ComparisonOperator::Exists {
                        return Err(invalid("query relation must use exists"));
                    }
                    let field = target
                        .strip_prefix("resource.")
                        .ok_or_else(|| invalid("query relation target must be resource field"))?;
                    let objects = state
                        .rebac_engine
                        .resolve_objects_live(ctx.tenant_id, &format!("user:{}", ctx.user_id), key)
                        .await?;
                    if objects.is_empty() {
                        return Err(AuthzError::ReBacDenied);
                    }
                    leaf.left = ValueSource::ResourceField {
                        key: field.to_owned(),
                    };
                    leaf.op = ComparisonOperator::In;
                    leaf.right = ValueSource::Literal {
                        value: serde_json::json!(objects),
                    };
                }
                Ok(ConditionNode::Leaf(
                    authz_core::models::policy::LeafCondition {
                        left: resolve_value(leaf.left, ctx)?,
                        op: leaf.op,
                        right: resolve_value(leaf.right, ctx)?,
                    },
                ))
            }
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn unsupported_sources_fail_closed() {
        let source = ValueSource::Relation {
            key: "member".into(),
            target: "owner".into(),
        };
        let ctx = AuthzContext {
            tenant_id: TenantId::new(),
            user_id: UserId::new(),
            user_attributes: serde_json::json!({}),
            user_attributes_version: 1,
            resource: ResourceContext {
                resource_type: "workflow".into(),
                resource_ref: None,
                attributes: serde_json::json!({}),
            },
            env: EnvContext::default(),
            backend: FilterBackend::Sql,
        };
        assert!(resolve_value(source, &ctx).is_err());
    }
}
