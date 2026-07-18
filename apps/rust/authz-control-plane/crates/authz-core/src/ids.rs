//! Typed newtype ID wrappers.
//!
//! Using newtypes prevents accidentally passing a `UserId` where a `TenantId`
//! is expected — caught at compile time, zero runtime cost.

use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// Macro to generate a typed UUID newtype.
macro_rules! uuid_newtype {
    ($name:ident, $doc:literal) => {
        #[doc = $doc]
        #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
        #[serde(transparent)]
        pub struct $name(pub Uuid);

        impl $name {
            /// Create a new random ID.
            pub fn new() -> Self {
                Self(Uuid::new_v4())
            }

            /// Wrap an existing UUID.
            pub fn from_uuid(id: Uuid) -> Self {
                Self(id)
            }

            /// Unwrap the inner UUID.
            pub fn into_uuid(self) -> Uuid {
                self.0
            }
        }

        impl Default for $name {
            fn default() -> Self {
                Self::new()
            }
        }

        impl std::fmt::Display for $name {
            fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                write!(f, "{}", self.0)
            }
        }

        impl From<Uuid> for $name {
            fn from(id: Uuid) -> Self {
                Self(id)
            }
        }

        impl From<$name> for Uuid {
            fn from(id: $name) -> Self {
                id.0
            }
        }
    };
}

uuid_newtype!(
    TenantId,
    "Identifies a tenant in a multi-tenant deployment."
);
uuid_newtype!(UserId, "Identifies a user account.");
uuid_newtype!(RoleId, "Identifies a role in the RBAC hierarchy.");
uuid_newtype!(PermissionId, "Identifies a permission definition.");
uuid_newtype!(ResourceTypeId, "Identifies a resource type definition.");
uuid_newtype!(
    ResourceInstanceId,
    "Identifies a specific resource instance with special ACL."
);
uuid_newtype!(PolicyId, "Identifies an authorization policy.");
uuid_newtype!(PolicyRuleId, "Identifies a rule within a policy.");
uuid_newtype!(
    PolicyVersionId,
    "Identifies a specific policy version snapshot."
);
uuid_newtype!(
    RelationTupleId,
    "Identifies a relation tuple in the ReBAC graph."
);
uuid_newtype!(FieldFilterId, "Identifies a field filter definition.");
uuid_newtype!(RowFilterId, "Identifies a row filter definition.");
uuid_newtype!(TemporalPolicyId, "Identifies a temporal access policy.");
uuid_newtype!(
    AuditLogId,
    "Identifies an authorization decision log entry."
);
uuid_newtype!(
    ExternalAttributeSourceId,
    "Identifies a registered external attribute source."
);
uuid_newtype!(
    SchemaFieldId,
    "Identifies a canonical field in the schema registry."
);
