//! Domain-level errors. Distinct from `AppError` (which is the HTTP boundary).

use thiserror::Error;

#[derive(Debug, Error)]
#[non_exhaustive]
pub enum DomainError {
    #[error("invalid organization code: {0}")]
    InvalidCode(String),

    #[error("invalid node kind transition: parent={parent:?}, child={child:?}")]
    InvalidKindHierarchy { parent: String, child: String },

    #[error("node not found in aggregate: {0}")]
    NodeNotFound(uuid::Uuid),

    #[error("move would create a cycle")]
    CycleDetected,

    #[error("aggregate version conflict: expected {expected}, found {found}")]
    VersionConflict { expected: i64, found: i64 },

    #[error("invariant violated: {0}")]
    Invariant(&'static str),
}
