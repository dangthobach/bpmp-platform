//! Domain events emitted by the Organization aggregate.
//!
//! Persisted via the Outbox in the **same transaction** as the state change,
//! then published by a background worker.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use super::{NodeId, NodeKind, OrgId};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum DomainEvent {
    OrganizationCreated {
        org_id: OrgId,
        tenant_id: Uuid,
        root_node_id: NodeId,
        at: DateTime<Utc>,
    },
    NodeAdded {
        org_id: OrgId,
        tenant_id: Uuid,
        node_id: NodeId,
        parent_id: NodeId,
        kind: NodeKind,
        code: String,
        path: String,
        at: DateTime<Utc>,
    },
    NodeMoved {
        org_id: OrgId,
        tenant_id: Uuid,
        node_id: NodeId,
        old_parent: NodeId,
        new_parent: NodeId,
        old_path: String,
        new_path: String,
        at: DateTime<Utc>,
    },
    NodeDeactivated {
        org_id: OrgId,
        tenant_id: Uuid,
        node_id: NodeId,
        at: DateTime<Utc>,
    },
}

impl DomainEvent {
    /// Event-type discriminator string (also used as Kafka topic suffix).
    pub fn event_type(&self) -> &'static str {
        match self {
            DomainEvent::OrganizationCreated { .. } => "organization.created",
            DomainEvent::NodeAdded { .. } => "organization.node.added",
            DomainEvent::NodeMoved { .. } => "organization.node.moved",
            DomainEvent::NodeDeactivated { .. } => "organization.node.deactivated",
        }
    }

    pub fn tenant_id(&self) -> Uuid {
        match self {
            DomainEvent::OrganizationCreated { tenant_id, .. }
            | DomainEvent::NodeAdded { tenant_id, .. }
            | DomainEvent::NodeMoved { tenant_id, .. }
            | DomainEvent::NodeDeactivated { tenant_id, .. } => *tenant_id,
        }
    }
}
