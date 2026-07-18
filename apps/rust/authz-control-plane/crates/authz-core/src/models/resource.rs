//! Resource registry models — Layer C.
//!
//! Supports the G1 design decision: type-level policies cover 90% of use cases;
//! instance-level ACL only created for resources needing special access control.

use crate::ids::{ResourceInstanceId, ResourceTypeId, TenantId, UserId};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;

/// Defines the structure and valid actions for a category of resources.
///
/// `schema_def` carries field mappings for multi-backend translation
/// and the canonical list of supported actions.
///
/// Example `schema_def`:
/// ```json
/// {
///   "attributes": ["branch_code", "status", "created_by"],
///   "actions": ["read", "write", "approve", "archive"],
///   "field_mappings": {
///     "branchCode": { "sql": "branch_code", "es": "branch_code", "mongo": "branchCode" }
///   }
/// }
/// ```
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResourceType {
    pub id: ResourceTypeId,
    pub tenant_id: TenantId,
    pub code: String,
    pub name: String,
    pub schema_def: ResourceSchemaDef,
    pub metadata: super::metadata::EntityMetadata,
}

impl ResourceType {
    /// Map a canonical field name to its backend-specific column/field name.
    ///
    /// Returns the canonical name itself if no specific mapping is registered
    /// for the given backend — safe default for simple cases.
    pub fn map_field(&self, canonical: &str, backend: &str) -> String {
        if let Some(m) = self.schema_def.field_mappings.get(canonical) {
            let mapped = match backend {
                "sql" => &m.sql,
                "es" => &m.es,
                "mongo" => &m.mongo,
                _ => &None,
            };
            if let Some(v) = mapped {
                return v.clone();
            }
        }
        canonical.to_owned()
    }
}

/// Schema definition stored in `resource_type.schema_def`.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ResourceSchemaDef {
    pub attributes: Vec<String>,
    pub actions: Vec<String>,
    pub field_mappings: std::collections::HashMap<String, FieldMapping>,
}

/// Backend-specific column/field name for a single canonical field.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct FieldMapping {
    pub sql: Option<String>,
    pub es: Option<String>,
    pub mongo: Option<String>,
}

/// A specific resource instance that has special ACL beyond type-level policy.
///
/// ## Important: only 1% of resources should have an instance record.
/// The 99% handled by type-level policy need NO row in this table.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResourceInstance {
    pub id: ResourceInstanceId,
    pub resource_type_id: ResourceTypeId,
    /// The domain service's own ID for this object.
    pub external_ref: Option<String>,
    pub owner_id: Option<UserId>,
    pub attributes: JsonValue,
    pub metadata: super::metadata::EntityMetadata,
}

/// An access control entry for a specific resource instance.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResourceAcl {
    pub id: uuid::Uuid,
    pub resource_instance_id: ResourceInstanceId,
    pub subject_id: uuid::Uuid,
    pub subject_type: AclSubjectType,
    /// Actions this ACL entry grants.
    pub actions: Vec<String>,
    /// Optional extra ABAC conditions on top of the ACL grant.
    pub conditions: Option<JsonValue>,
    pub metadata: super::metadata::EntityMetadata,
}

/// The type of subject referenced in an ACL entry.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum AclSubjectType {
    User,
    Role,
    Group,
}

/// A canonical field registered in the schema field registry.
///
/// Used by policy validation (CI) to catch unknown field references.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SchemaField {
    pub id: crate::ids::SchemaFieldId,
    pub tenant_id: TenantId,
    pub resource_type: String,
    /// Canonical name used in AST policies.
    pub canonical_name: String,
    pub sql_name: String,
    pub es_name: Option<String>,
    pub mongo_name: Option<String>,
    pub data_type: FieldDataType,
    pub enum_values: Option<Vec<String>>,
    pub description: Option<String>,
    pub metadata: super::metadata::EntityMetadata,
}

/// Supported data types for schema fields.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum FieldDataType {
    String,
    Uuid,
    Timestamp,
    Integer,
    Boolean,
    Enum,
    JsonObject,
}
