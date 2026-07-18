//! Canonical persistence metadata for mutable control-plane entities.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// Metadata shared by every mutable entity stored by the authorization control plane.
///
/// `version` is an optimistic-lock token. Pessimistic locking is a repository
/// concern implemented with `SELECT ... FOR UPDATE` in a short transaction.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EntityMetadata {
    pub version: i64,
    pub is_deleted: bool,
    pub deleted_at: Option<DateTime<Utc>>,
    pub deleted_by: Option<Uuid>,
    pub created_at: DateTime<Utc>,
    pub created_by: Option<Uuid>,
    pub updated_at: DateTime<Utc>,
    pub updated_by: Option<Uuid>,
}

impl EntityMetadata {
    #[allow(clippy::too_many_arguments)]
    pub fn from_persistence(
        version: i64,
        is_deleted: bool,
        deleted_at: Option<DateTime<Utc>>,
        deleted_by: Option<Uuid>,
        created_at: DateTime<Utc>,
        created_by: Option<Uuid>,
        updated_at: DateTime<Utc>,
        updated_by: Option<Uuid>,
    ) -> Self {
        Self {
            version,
            is_deleted,
            deleted_at,
            deleted_by,
            created_at,
            created_by,
            updated_at,
            updated_by,
        }
    }
}
