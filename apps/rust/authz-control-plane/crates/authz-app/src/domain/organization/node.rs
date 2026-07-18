//! Entities inside the Organization aggregate.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

use super::path::MaterializedPath;
use super::NodeId;
use crate::domain::errors::DomainError;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum NodeKind {
    Group,
    Subsidiary,
    Branch,
    Department,
}

impl NodeKind {
    pub fn as_str(&self) -> &'static str {
        match self {
            NodeKind::Group => "GROUP",
            NodeKind::Subsidiary => "SUBSIDIARY",
            NodeKind::Branch => "BRANCH",
            NodeKind::Department => "DEPARTMENT",
        }
    }

    /// Allowed parent → child transitions.
    /// `Group → Subsidiary → Branch → Department`.
    /// Skipping levels is disallowed to keep the tree semantically uniform.
    pub fn allows_child(self, child: NodeKind) -> bool {
        matches!(
            (self, child),
            (NodeKind::Group, NodeKind::Subsidiary)
                | (NodeKind::Subsidiary, NodeKind::Branch)
                | (NodeKind::Branch, NodeKind::Department)
        )
    }

    pub fn validate_child(self, child: NodeKind) -> Result<(), DomainError> {
        if self.allows_child(child) {
            Ok(())
        } else {
            Err(DomainError::InvalidKindHierarchy {
                parent: self.as_str().to_owned(),
                child: child.as_str().to_owned(),
            })
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrgNode {
    pub id: NodeId,
    pub parent_id: Option<NodeId>,
    pub code: String,
    pub name: String,
    pub kind: NodeKind,
    pub path: MaterializedPath,
    pub is_active: bool,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}
