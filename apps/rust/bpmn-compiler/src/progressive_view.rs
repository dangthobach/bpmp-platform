use std::collections::BTreeSet;

use bpmp_contracts::wir::v1::{ExtensionProperty, Node, WorkflowIntermediateRepresentation, node};
use thiserror::Error;

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum ViewMode {
    BusinessAnalyst,
    Engineer,
}

/// Owns one canonical WIR and derives both progressive views from that source.
pub struct ProgressiveModel {
    wir: WorkflowIntermediateRepresentation,
}

impl ProgressiveModel {
    /// Creates progressive views after validating stable node identity.
    ///
    /// # Errors
    ///
    /// Rejects empty or duplicate node IDs.
    pub fn new(wir: WorkflowIntermediateRepresentation) -> Result<Self, ProgressiveViewError> {
        let mut ids = BTreeSet::new();
        if wir
            .nodes
            .iter()
            .any(|node| node.id.trim().is_empty() || !ids.insert(node.id.clone()))
        {
            return Err(ProgressiveViewError::InvalidNodeIdentity);
        }
        Ok(Self { wir })
    }

    #[must_use]
    pub fn wir(&self) -> &WorkflowIntermediateRepresentation {
        &self.wir
    }

    /// Returns borrowed nodes from the canonical WIR without duplicating state.
    #[must_use]
    pub fn nodes(&self, mode: ViewMode) -> Vec<&Node> {
        self.wir
            .nodes
            .iter()
            .filter(|node| mode == ViewMode::Engineer || !is_technical(node))
            .collect()
    }

    /// Applies a view edit to the canonical WIR by stable node ID.
    ///
    /// # Errors
    ///
    /// Rejects missing node IDs and duplicate extension-property identities.
    pub fn set_property(
        &mut self,
        node_id: &str,
        property: ExtensionProperty,
    ) -> Result<(), ProgressiveViewError> {
        let node = self
            .wir
            .nodes
            .iter_mut()
            .find(|node| node.id == node_id)
            .ok_or(ProgressiveViewError::MissingNode)?;
        let identity = (
            property.namespace_uri.as_str(),
            property.element_name.as_str(),
            property.name.as_str(),
        );
        if node.properties.iter().any(|existing| {
            (
                existing.namespace_uri.as_str(),
                existing.element_name.as_str(),
                existing.name.as_str(),
            ) == identity
        }) {
            return Err(ProgressiveViewError::DuplicateProperty);
        }
        node.properties.push(property);
        Ok(())
    }
}

fn is_technical(node: &Node) -> bool {
    matches!(
        node.kind,
        Some(
            node::Kind::ServiceTask(_)
                | node::Kind::ScriptTask(_)
                | node::Kind::DecisionTask(_)
                | node::Kind::ExclusiveGateway(_)
                | node::Kind::InclusiveGateway(_)
                | node::Kind::ParallelGateway(_)
        )
    )
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum ProgressiveViewError {
    #[error("WIR contains an empty or duplicate node ID")]
    InvalidNodeIdentity,
    #[error("progressive view edit references a missing node")]
    MissingNode,
    #[error("extension property identity already exists on this node")]
    DuplicateProperty,
}

#[cfg(test)]
mod tests {
    use bpmp_contracts::wir::v1::{
        EndNode, PropertyValue, ServiceTaskNode, StartNode, UserTaskNode, property_value,
    };
    use proptest::prelude::*;

    use super::*;

    fn wir(node_count: u8) -> WorkflowIntermediateRepresentation {
        let mut nodes = vec![Node {
            id: "start".into(),
            kind: Some(node::Kind::Start(StartNode {
                next_node_id: "end".into(),
            })),
            ..Node::default()
        }];
        for index in 0..node_count {
            let technical = index % 2 == 0;
            nodes.push(Node {
                id: format!("node-{index}"),
                kind: Some(if technical {
                    node::Kind::ServiceTask(ServiceTaskNode {
                        task_type: "adapter".into(),
                        next_node_id: "end".into(),
                    })
                } else {
                    node::Kind::UserTask(UserTaskNode {
                        task_type: "approval".into(),
                        next_node_id: "end".into(),
                        assignment_policy_ref: "approvers".into(),
                        form_key: String::new(),
                        result_variable: String::new(),
                    })
                }),
                ..Node::default()
            });
        }
        nodes.push(Node {
            id: "end".into(),
            kind: Some(node::Kind::End(EndNode {})),
            ..Node::default()
        });
        WorkflowIntermediateRepresentation {
            schema_version: 1,
            workflow_type: "order".into(),
            workflow_version: "1".into(),
            start_node_id: "start".into(),
            nodes,
            tenant_id: "tenant-a".into(),
            ..WorkflowIntermediateRepresentation::default()
        }
    }

    proptest! {
        #![proptest_config(ProptestConfig::with_cases(100))]

        // Feature: rust-bpm-platform, Property 21: Progressive views share one lossless WIR identity space
        #[test]
        fn view_sync_preserves_every_unique_node_id(count in 1_u8..50) {
            let mut model = ProgressiveModel::new(wir(count)).unwrap();
            let target = if count > 1 { "node-1" } else { "start" };
            model.set_property(
                target,
                ExtensionProperty {
                    namespace_uri: "urn:bpmp:view".into(),
                    element_name: "annotation".into(),
                    name: "label".into(),
                    value: Some(PropertyValue {
                        value: Some(property_value::Value::StringValue("review".into())),
                    }),
                },
            ).unwrap();
            let engineer_ids = model.nodes(ViewMode::Engineer)
                .into_iter()
                .map(|node| node.id.clone())
                .collect::<BTreeSet<_>>();
            prop_assert_eq!(engineer_ids.len(), model.wir().nodes.len());
            prop_assert!(model.wir().nodes.iter().find(|node| node.id == target).unwrap().properties.len() == 1);
        }

        // Feature: rust-bpm-platform, Property 22: BA view hides technical nodes and Engineer view includes all
        #[test]
        fn business_view_hides_only_technical_nodes(count in 1_u8..50) {
            let model = ProgressiveModel::new(wir(count)).unwrap();
            let business = model.nodes(ViewMode::BusinessAnalyst);
            let engineer = model.nodes(ViewMode::Engineer);
            prop_assert_eq!(engineer.len(), usize::from(count) + 2);
            prop_assert!(business.iter().all(|node| !is_technical(node)));
            prop_assert_eq!(
                business.len(),
                engineer.iter().filter(|node| !is_technical(node)).count()
            );
        }
    }
}
