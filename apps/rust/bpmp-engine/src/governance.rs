use bpmp_domain_core::{
    ActorId, CommandId, ConfigVersion, CorrelationId, InstanceId, InstanceState, KeyScope,
    Lifecycle, PolicyVersion, TenantId, WorkflowType, WorkflowVersion, evolve,
};
use bpmp_governance_domain::{
    AbortAndReconcileDecision, AbortAndReconcileRequest, CompensationLedgerEntry,
    CompensationStatus, DualControlProof, GovernanceError, GovernancePolicy,
    decide_abort_and_reconcile,
};
use thiserror::Error;

use crate::{EVENT_SCHEMA_VERSION, EventEnvelope, EventMetadata, SnapshotEnvelope};

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct GovernanceCommandContext {
    pub tenant_id: TenantId,
    pub instance_id: InstanceId,
    pub command_id: CommandId,
    pub correlation_id: CorrelationId,
    pub workflow_type: WorkflowType,
    pub workflow_version: WorkflowVersion,
    pub config_version: ConfigVersion,
    pub policy_version: PolicyVersion,
    pub operational_key_scope: KeyScope,
    pub expected_version: u64,
    pub evaluated_at_epoch_ms: u64,
    pub occurred_at_epoch_ms: u64,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct GovernanceTransitionPlan {
    pub event: EventEnvelope,
    pub snapshot: SnapshotEnvelope,
    pub ledger_updates: Vec<CompensationLedgerEntry>,
    pub decision: AbortAndReconcileDecision,
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum GovernanceTransitionError {
    #[error(transparent)]
    Governance(#[from] GovernanceError),
    #[error("governance request scope does not match the authoritative stream")]
    ScopeMismatch,
    #[error("governance expected version {expected} does not match state version {actual}")]
    VersionMismatch { expected: u64, actual: u64 },
    #[error("governance transition cannot be applied to an initial workflow")]
    WorkflowNotStarted,
    #[error("governance transition cannot be applied to an already terminal workflow")]
    WorkflowAlreadyTerminal,
    #[error("governance requester actor identifier is invalid")]
    InvalidRequesterActor,
    #[error("governance event sequence overflow")]
    SequenceOverflow,
    #[error("reconciliation work-item count exceeds the durable event field")]
    ReconciliationCountOverflow,
    #[error("compensation ledger sequence overflow")]
    LedgerSequenceOverflow,
}

/// Produces the complete deterministic governance transition before encryption
/// and Raft proposal. No external state is read here.
///
/// # Errors
///
/// Fails closed for stale scope/version/ledger/proof or an ineligible lifecycle.
pub fn prepare_abort_and_reconcile(
    state: &InstanceState,
    request: &AbortAndReconcileRequest,
    pending_entries: &[CompensationLedgerEntry],
    proof: &DualControlProof,
    policy: &GovernancePolicy,
    context: &GovernanceCommandContext,
) -> Result<GovernanceTransitionPlan, GovernanceTransitionError> {
    if request.tenant_id != context.tenant_id.as_str()
        || request.instance_id != context.instance_id.as_str()
        || pending_entries.iter().any(|entry| {
            entry.tenant_id != context.tenant_id.as_str()
                || entry.instance_id != context.instance_id.as_str()
        })
    {
        return Err(GovernanceTransitionError::ScopeMismatch);
    }
    if state.sequence != context.expected_version {
        return Err(GovernanceTransitionError::VersionMismatch {
            expected: context.expected_version,
            actual: state.sequence,
        });
    }
    match state.lifecycle {
        Lifecycle::Initial => return Err(GovernanceTransitionError::WorkflowNotStarted),
        Lifecycle::Completed | Lifecycle::TerminatedForCompliance => {
            return Err(GovernanceTransitionError::WorkflowAlreadyTerminal);
        }
        Lifecycle::Active { .. } => {}
    }

    let decision = decide_abort_and_reconcile(
        request,
        pending_entries,
        proof,
        policy,
        context.evaluated_at_epoch_ms,
    )?;
    let actor_id = ActorId::new(proof.requester.actor_id.clone())
        .map_err(|_| GovernanceTransitionError::InvalidRequesterActor)?;
    let sequence = context
        .expected_version
        .checked_add(1)
        .ok_or(GovernanceTransitionError::SequenceOverflow)?;
    let reconciliation_count = u32::try_from(decision.work_items.len())
        .map_err(|_| GovernanceTransitionError::ReconciliationCountOverflow)?;
    let event = EventEnvelope {
        metadata: EventMetadata {
            event_id: format!("{}:{sequence}", context.command_id),
            tenant_id: context.tenant_id.clone(),
            instance_id: context.instance_id.clone(),
            sequence,
            schema_version: EVENT_SCHEMA_VERSION,
            correlation_id: context.correlation_id.clone(),
            causation_command_id: context.command_id.clone(),
            occurred_at_epoch_ms: context.occurred_at_epoch_ms,
            config_version: context.config_version.clone(),
            policy_version: context.policy_version.clone(),
            actor_id,
            encryption_key_scope: context.operational_key_scope.clone(),
            workflow_type: context.workflow_type.clone(),
            workflow_version: context.workflow_version.clone(),
        },
        event: bpmp_domain_core::DomainEvent::WorkflowTerminatedForCompliance {
            policy_id: request.policy_id.clone(),
            request_digest: decision.request_digest,
            reason_code: request.reason_code.clone(),
            reconciliation_count,
            occurred_at_epoch_ms: context.occurred_at_epoch_ms,
        },
    };
    let terminated_state = evolve(state.clone(), &event.event);
    let snapshot = SnapshotEnvelope {
        tenant_id: context.tenant_id.clone(),
        instance_id: context.instance_id.clone(),
        workflow_type: context.workflow_type.clone(),
        workflow_version: context.workflow_version.clone(),
        state: terminated_state,
        config_version: context.config_version.clone(),
        policy_version: context.policy_version.clone(),
        encryption_key_scope: context.operational_key_scope.clone(),
    };
    let ledger_updates = pending_entries
        .iter()
        .cloned()
        .map(|mut entry| {
            entry.ledger_sequence = entry
                .ledger_sequence
                .checked_add(1)
                .ok_or(GovernanceTransitionError::LedgerSequenceOverflow)?;
            entry.status = CompensationStatus::ReconciliationRequired;
            entry.updated_at_epoch_ms = context.occurred_at_epoch_ms;
            Ok(entry)
        })
        .collect::<Result<_, GovernanceTransitionError>>()?;

    Ok(GovernanceTransitionPlan {
        event,
        snapshot,
        ledger_updates,
        decision,
    })
}

#[cfg(test)]
mod tests {
    use std::collections::{BTreeMap, BTreeSet};

    use bpmp_governance_domain::{
        SignedApproval, abort_request_digest, approval_signing_payload, pending_ledger_digest,
    };
    use ed25519_dalek::{Signer as _, SigningKey};

    use super::*;

    fn ledger() -> Vec<CompensationLedgerEntry> {
        vec![CompensationLedgerEntry {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-a".into(),
            saga_ref: "saga-a".into(),
            ledger_entry_id: "ledger-1".into(),
            effect_sequence: 1,
            ledger_sequence: 1,
            side_effect_type: "payment".into(),
            target_system: "bank".into(),
            handler_ref: "refund-v1".into(),
            opaque_operation_ref: "opaque-operation".into(),
            idempotency_key: "effect-idempotency".into(),
            status: CompensationStatus::Pending,
            updated_at_epoch_ms: 10,
        }]
    }

    fn approval(actor: &str, key_id: &str, key: &SigningKey, digest: [u8; 32]) -> SignedApproval {
        let mut approval = SignedApproval {
            request_digest: digest,
            tenant_id: "tenant-a".into(),
            actor_id: actor.into(),
            capability: "configured-abort".into(),
            auth_assurance: "configured-high".into(),
            approved_at_epoch_ms: 100,
            expires_at_epoch_ms: 200,
            key_id: key_id.into(),
            signature: Vec::new(),
        };
        approval.signature = key
            .sign(&approval_signing_payload(&approval))
            .to_bytes()
            .to_vec();
        approval
    }

    #[test]
    fn prepares_terminal_event_snapshot_and_ledger_updates_from_valid_proof() {
        let requester_key = SigningKey::from_bytes(&[1; 32]);
        let approver_key = SigningKey::from_bytes(&[2; 32]);
        let entries = ledger();
        let request = AbortAndReconcileRequest {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-a".into(),
            policy_id: "policy-1".into(),
            legal_deadline_epoch_ms: 500,
            key_scope: "subject-key".into(),
            key_epoch: 7,
            pending_ledger_digest: pending_ledger_digest(&entries),
            reason_code: "legal-deadline".into(),
        };
        let digest = abort_request_digest(&request);
        let proof = DualControlProof {
            requester: approval("requester", "requester-key", &requester_key, digest),
            approvers: vec![approval("approver", "approver-key", &approver_key, digest)],
        };
        let policy = GovernancePolicy {
            abort_capability: "configured-abort".into(),
            accepted_auth_assurance: BTreeSet::from(["configured-high".into()]),
            approval_public_keys: BTreeMap::from([
                (
                    "requester-key".into(),
                    requester_key.verifying_key().to_bytes(),
                ),
                (
                    "approver-key".into(),
                    approver_key.verifying_key().to_bytes(),
                ),
            ]),
            required_approver_count: 1,
            max_proof_age_ms: 50,
            max_approval_ttl_ms: 100,
            max_pending_ledger_entries: 8,
        };
        let state = InstanceState {
            lifecycle: Lifecycle::Active {
                active_node: bpmp_domain_core::NodeId::new("task").unwrap(),
            },
            sequence: 4,
            ..InstanceState::default()
        };
        let context = GovernanceCommandContext {
            tenant_id: TenantId::new("tenant-a").unwrap(),
            instance_id: InstanceId::new("instance-a").unwrap(),
            command_id: CommandId::new("governance-command").unwrap(),
            correlation_id: CorrelationId::new("correlation").unwrap(),
            workflow_type: WorkflowType::new("order").unwrap(),
            workflow_version: WorkflowVersion::new("1").unwrap(),
            config_version: ConfigVersion::new("config-1").unwrap(),
            policy_version: PolicyVersion::new("policy-1").unwrap(),
            operational_key_scope: KeyScope::new("tenant-a/governance").unwrap(),
            expected_version: 4,
            evaluated_at_epoch_ms: 125,
            occurred_at_epoch_ms: 130,
        };

        let plan =
            prepare_abort_and_reconcile(&state, &request, &entries, &proof, &policy, &context)
                .unwrap();

        assert_eq!(plan.event.metadata.sequence, 5);
        assert_eq!(
            plan.snapshot.state.lifecycle,
            Lifecycle::TerminatedForCompliance
        );
        assert_eq!(plan.decision.work_items.len(), 1);
        assert_eq!(
            plan.ledger_updates[0].status,
            CompensationStatus::ReconciliationRequired
        );
        assert!(matches!(
            plan.event.event,
            bpmp_domain_core::DomainEvent::WorkflowTerminatedForCompliance {
                reconciliation_count: 1,
                ..
            }
        ));
    }
}
