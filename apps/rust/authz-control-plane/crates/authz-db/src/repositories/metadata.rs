use authz_core::models::metadata::EntityMetadata;
use chrono::{DateTime, Utc};
use uuid::Uuid;

#[derive(sqlx::FromRow)]
pub(super) struct MetadataRow {
    pub version: i64,
    pub is_deleted: bool,
    pub deleted_at: Option<DateTime<Utc>>,
    pub deleted_by: Option<Uuid>,
    pub created_at: DateTime<Utc>,
    pub created_by: Option<Uuid>,
    pub updated_at: DateTime<Utc>,
    pub updated_by: Option<Uuid>,
}

impl From<MetadataRow> for EntityMetadata {
    fn from(row: MetadataRow) -> Self {
        Self::from_persistence(
            row.version,
            row.is_deleted,
            row.deleted_at,
            row.deleted_by,
            row.created_at,
            row.created_by,
            row.updated_at,
            row.updated_by,
        )
    }
}
