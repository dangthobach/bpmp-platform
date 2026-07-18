//! Organization bounded context — root aggregate + value objects.

mod aggregate;
mod events;
mod node;
mod path;

pub use aggregate::Organization;
pub use events::DomainEvent;
pub use node::{NodeKind, OrgNode};
pub use path::MaterializedPath;

use serde::{Deserialize, Serialize};
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(transparent)]
pub struct OrgId(pub Uuid);

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(transparent)]
pub struct NodeId(pub Uuid);

impl OrgId {
    pub fn new() -> Self {
        Self(Uuid::new_v4())
    }
}
impl NodeId {
    pub fn new() -> Self {
        Self(Uuid::new_v4())
    }
}
impl Default for OrgId {
    fn default() -> Self {
        Self::new()
    }
}
impl Default for NodeId {
    fn default() -> Self {
        Self::new()
    }
}
