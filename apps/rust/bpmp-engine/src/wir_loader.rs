use bpmp_contracts::wir::v1::{WorkflowIntermediateRepresentation, node};
use bpmp_contracts::{ArtifactError, WirArtifactVerifier, WirCodec};
use bpmp_domain_core::{
    DomainError, IdentifierError, Node, NodeId, TaskType, WorkflowDefinition, WorkflowType,
    WorkflowVersion,
};
use thiserror::Error;

pub struct WirLoader;

impl WirLoader {
    /// Verifies and maps a canonical WIR artifact into validated engine domain data.
    ///
    /// # Errors
    ///
    /// Returns [`WirLoadError`] when integrity/schema validation fails or the
    /// decoded graph cannot construct a valid workflow definition.
    pub fn load(
        artifact: &[u8],
        verifier: &dyn WirArtifactVerifier,
    ) -> Result<WorkflowDefinition, WirLoadError> {
        let wir = WirCodec::open(artifact, verifier)?;
        map_definition(wir)
    }
}

fn map_definition(
    wir: WorkflowIntermediateRepresentation,
) -> Result<WorkflowDefinition, WirLoadError> {
    let workflow_type =
        WorkflowType::new(wir.workflow_type).map_err(|source| WirLoadError::Identifier {
            field: "workflow_type",
            source,
        })?;
    let workflow_version =
        WorkflowVersion::new(wir.workflow_version).map_err(|source| WirLoadError::Identifier {
            field: "workflow_version",
            source,
        })?;
    let start_node = NodeId::new(wir.start_node_id).map_err(|source| WirLoadError::Identifier {
        field: "start_node_id",
        source,
    })?;
    let mut nodes = Vec::with_capacity(wir.nodes.len());
    for encoded in wir.nodes {
        let node_id = NodeId::new(encoded.id).map_err(|source| WirLoadError::Identifier {
            field: "node.id",
            source,
        })?;
        let kind = match encoded
            .kind
            .ok_or_else(|| WirLoadError::MissingNodeKind(node_id.clone()))?
        {
            node::Kind::Start(start) => Node::Start {
                next: node_id_value(start.next_node_id, "start.next_node_id")?,
            },
            node::Kind::ServiceTask(task) => Node::ServiceTask {
                task_type: TaskType::new(task.task_type).map_err(|source| {
                    WirLoadError::Identifier {
                        field: "service_task.task_type",
                        source,
                    }
                })?,
                next: node_id_value(task.next_node_id, "service_task.next_node_id")?,
            },
            node::Kind::End(_) => Node::End,
        };
        nodes.push((node_id, kind));
    }
    WorkflowDefinition::new(workflow_type, workflow_version, start_node, nodes)
        .map_err(WirLoadError::Domain)
}

fn node_id_value(value: String, field: &'static str) -> Result<NodeId, WirLoadError> {
    NodeId::new(value).map_err(|source| WirLoadError::Identifier { field, source })
}

#[derive(Debug, Error)]
pub enum WirLoadError {
    #[error(transparent)]
    Artifact(#[from] ArtifactError),
    #[error("invalid identifier in WIR field {field}: {source}")]
    Identifier {
        field: &'static str,
        source: IdentifierError,
    },
    #[error("WIR node {0} has no kind")]
    MissingNodeKind(NodeId),
    #[error(transparent)]
    Domain(DomainError),
}
