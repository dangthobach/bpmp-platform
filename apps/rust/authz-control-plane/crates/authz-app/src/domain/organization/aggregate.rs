//! `Organization` aggregate root.
//!
//! Owns:
//! * A set of `OrgNode`s indexed by `NodeId` (HashMap → O(1) lookup).
//! * The tree-shape invariants: kind hierarchy, single root, no cycles,
//!   path consistency.
//! * Optimistic-lock `version` — bumped on every state-changing operation.
//!
//! The aggregate produces [`DomainEvent`]s; persistence is the repository's job.

use std::collections::HashMap;

use chrono::Utc;
use uuid::Uuid;

use super::events::DomainEvent;
use super::node::{NodeKind, OrgNode};
use super::path::MaterializedPath;
use super::{NodeId, OrgId};
use crate::domain::errors::DomainError;

pub struct Organization {
    id: OrgId,
    tenant_id: Uuid,
    root_id: NodeId,
    nodes: HashMap<NodeId, OrgNode>,
    version: i64,
}

impl Organization {
    /// Hydrate from persistence — caller must guarantee invariants already hold.
    pub fn hydrate(
        id: OrgId,
        tenant_id: Uuid,
        root_id: NodeId,
        nodes: Vec<OrgNode>,
        version: i64,
    ) -> Self {
        let nodes = nodes.into_iter().map(|n| (n.id, n)).collect();
        Self {
            id,
            tenant_id,
            root_id,
            nodes,
            version,
        }
    }

    pub fn create_root(
        tenant_id: Uuid,
        code: String,
        name: String,
    ) -> Result<(Self, DomainEvent), DomainError> {
        let org_id = OrgId::new();
        let node_id = NodeId::new();
        let now = Utc::now();
        let path = MaterializedPath::new(code.to_lowercase())?;
        let root = OrgNode {
            id: node_id,
            parent_id: None,
            code,
            name,
            kind: NodeKind::Group,
            path,
            is_active: true,
            created_at: now,
            updated_at: now,
        };
        let mut nodes = HashMap::new();
        nodes.insert(node_id, root);
        let org = Self {
            id: org_id,
            tenant_id,
            root_id: node_id,
            nodes,
            version: 0,
        };
        let evt = DomainEvent::OrganizationCreated {
            org_id,
            tenant_id,
            root_node_id: node_id,
            at: now,
        };
        Ok((org, evt))
    }

    pub fn id(&self) -> OrgId {
        self.id
    }
    pub fn tenant_id(&self) -> Uuid {
        self.tenant_id
    }
    pub fn version(&self) -> i64 {
        self.version
    }
    pub fn nodes(&self) -> impl Iterator<Item = &OrgNode> {
        self.nodes.values()
    }

    pub fn add_node(
        &mut self,
        parent_id: NodeId,
        kind: NodeKind,
        code: String,
        name: String,
    ) -> Result<DomainEvent, DomainError> {
        let parent = self
            .nodes
            .get(&parent_id)
            .ok_or(DomainError::NodeNotFound(parent_id.0))?;
        parent.kind.validate_child(kind)?;
        let path = parent.path.child(&code.to_lowercase())?;
        let node_id = NodeId::new();
        let now = Utc::now();
        let node = OrgNode {
            id: node_id,
            parent_id: Some(parent_id),
            code: code.clone(),
            name,
            kind,
            path: path.clone(),
            is_active: true,
            created_at: now,
            updated_at: now,
        };
        self.nodes.insert(node_id, node);
        self.version += 1;
        Ok(DomainEvent::NodeAdded {
            org_id: self.id,
            tenant_id: self.tenant_id,
            node_id,
            parent_id,
            kind,
            code,
            path: path.as_str().to_owned(),
            at: now,
        })
    }

    pub fn move_node(
        &mut self,
        node_id: NodeId,
        new_parent_id: NodeId,
    ) -> Result<DomainEvent, DomainError> {
        if node_id == self.root_id {
            return Err(DomainError::Invariant("cannot move root node"));
        }
        let node_path = self
            .nodes
            .get(&node_id)
            .ok_or(DomainError::NodeNotFound(node_id.0))?
            .path
            .clone();
        let new_parent = self
            .nodes
            .get(&new_parent_id)
            .ok_or(DomainError::NodeNotFound(new_parent_id.0))?;
        // Cycle: new parent must not be inside the moved subtree.
        if node_path.is_ancestor_of(&new_parent.path) {
            return Err(DomainError::CycleDetected);
        }
        let new_parent_path = new_parent.path.clone();
        let new_parent_kind = new_parent.kind;

        let old_node = self.nodes.get(&node_id).expect("checked above");
        new_parent_kind.validate_child(old_node.kind)?;
        let old_parent_id = old_node
            .parent_id
            .ok_or(DomainError::Invariant("orphan node"))?;
        let old_path_node = old_node.path.clone();
        let new_path_node = new_parent_path.child(&old_node.code.to_lowercase())?;

        // Apply rename to subtree (including the node itself).
        let updates: Vec<(NodeId, MaterializedPath)> = self
            .nodes
            .values()
            .filter(|n| old_path_node.is_ancestor_of(&n.path))
            .map(|n| {
                (
                    n.id,
                    n.path.reparent(&old_path_node, &new_path_node).unwrap(),
                )
            })
            .collect();

        let now = Utc::now();
        for (id, p) in updates {
            if let Some(n) = self.nodes.get_mut(&id) {
                n.path = p;
                n.updated_at = now;
                if n.id == node_id {
                    n.parent_id = Some(new_parent_id);
                }
            }
        }
        self.version += 1;
        Ok(DomainEvent::NodeMoved {
            org_id: self.id,
            tenant_id: self.tenant_id,
            node_id,
            old_parent: old_parent_id,
            new_parent: new_parent_id,
            old_path: old_path_node.as_str().to_owned(),
            new_path: new_path_node.as_str().to_owned(),
            at: now,
        })
    }
}
