//! Pure governance rules for compliance termination and reconciliation.
//!
//! This crate performs no I/O and reads no ambient clock or configuration.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt::Write as _;

use ed25519_dalek::{Signature, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};
use thiserror::Error;

pub type Digest = [u8; 32];

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct GovernancePolicy {
    pub abort_capability: String,
    pub accepted_auth_assurance: BTreeSet<String>,
    pub approval_public_keys: BTreeMap<String, [u8; 32]>,
    pub required_approver_count: u16,
    pub max_proof_age_ms: u64,
    pub max_approval_ttl_ms: u64,
    pub max_pending_ledger_entries: u32,
}

impl GovernancePolicy {
    /// Validates a versioned policy resolved by the application layer.
    ///
    /// # Errors
    ///
    /// Returns [`GovernanceError`] when the policy could permit an ambiguous or
    /// unbounded governance decision.
    pub fn validate(&self) -> Result<(), GovernanceError> {
        if self.abort_capability.trim().is_empty()
            || self.accepted_auth_assurance.is_empty()
            || self.approval_public_keys.is_empty()
            || self.required_approver_count == 0
            || self.max_proof_age_ms == 0
            || self.max_approval_ttl_ms == 0
            || self.max_pending_ledger_entries == 0
        {
            return Err(GovernanceError::InvalidPolicy);
        }
        if self
            .approval_public_keys
            .keys()
            .any(|key_id| key_id.trim().is_empty())
            || self
                .accepted_auth_assurance
                .iter()
                .any(|assurance| assurance.trim().is_empty())
        {
            return Err(GovernanceError::InvalidPolicy);
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq, Serialize, Deserialize)]
pub enum CompensationStatus {
    Pending,
    Compensated,
    ReconciliationRequired,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct CompensationLedgerEntry {
    pub tenant_id: String,
    pub instance_id: String,
    pub saga_ref: String,
    pub ledger_entry_id: String,
    pub effect_sequence: u64,
    pub ledger_sequence: u64,
    pub side_effect_type: String,
    pub target_system: String,
    pub handler_ref: String,
    pub opaque_operation_ref: String,
    pub idempotency_key: String,
    pub status: CompensationStatus,
    pub updated_at_epoch_ms: u64,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct AbortAndReconcileRequest {
    pub tenant_id: String,
    pub instance_id: String,
    pub policy_id: String,
    pub legal_deadline_epoch_ms: u64,
    pub key_scope: String,
    pub key_epoch: u64,
    pub pending_ledger_digest: Digest,
    pub reason_code: String,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct SignedApproval {
    pub request_digest: Digest,
    pub tenant_id: String,
    pub actor_id: String,
    pub capability: String,
    pub auth_assurance: String,
    pub approved_at_epoch_ms: u64,
    pub expires_at_epoch_ms: u64,
    pub key_id: String,
    pub signature: Vec<u8>,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct DualControlProof {
    pub requester: SignedApproval,
    pub approvers: Vec<SignedApproval>,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct ReconciliationWorkItem {
    pub tenant_id: String,
    pub instance_id: String,
    pub reconciliation_id: String,
    pub ledger_entry_id: String,
    pub side_effect_type: String,
    pub target_system: String,
    pub handler_ref: String,
    pub opaque_operation_ref: String,
    pub deadline_epoch_ms: u64,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct GovernanceAuditRef {
    pub actor_id: String,
    pub approved_at_epoch_ms: u64,
    pub key_id: String,
}

#[derive(Debug, Clone, Eq, PartialEq, Serialize, Deserialize)]
pub struct AbortAndReconcileDecision {
    pub request_digest: Digest,
    pub ledger_digest: Digest,
    pub requester_audit: GovernanceAuditRef,
    pub approver_audits: Vec<GovernanceAuditRef>,
    pub work_items: Vec<ReconciliationWorkItem>,
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum GovernanceError {
    #[error("governance policy is incomplete or unbounded")]
    InvalidPolicy,
    #[error("governance request contains an empty or invalid field")]
    InvalidRequest,
    #[error("pending compensation ledger exceeds configured bound {configured_limit}")]
    PendingLedgerLimitExceeded { configured_limit: u32 },
    #[error("compensation ledger does not match the request scope")]
    LedgerScopeMismatch,
    #[error("compensation ledger contains a non-pending entry")]
    LedgerEntryNotPending,
    #[error("pending compensation entry lacks reconciliation metadata")]
    MissingReconciliationMetadata,
    #[error("pending compensation ledger contains duplicate identities or ordering keys")]
    DuplicateLedgerEntry,
    #[error("pending ledger digest is stale")]
    StaleLedgerDigest,
    #[error("dual-control proof is bound to another request")]
    ProofDigestMismatch,
    #[error("dual-control proof has fewer approvers than configured")]
    InsufficientApprovers,
    #[error("requester and approvers must be distinct actors")]
    ActorSeparationViolation,
    #[error("approval tenant does not match the request")]
    ApprovalTenantMismatch,
    #[error("approval lacks the configured governance capability")]
    CapabilityDenied,
    #[error("approval authentication assurance is not accepted by policy")]
    AuthenticationAssuranceDenied,
    #[error("approval is not valid at the injected evaluation time")]
    ApprovalOutsideValidity,
    #[error("approval lifetime exceeds configured maximum")]
    ApprovalLifetimeExceeded,
    #[error("approval signing key is unknown or malformed")]
    UnknownApprovalKey,
    #[error("approval signature is invalid")]
    InvalidApprovalSignature,
}

/// Computes the canonical digest of the current pending ledger set.
///
/// Input order does not affect the result. Every field needed to create a
/// non-PII reconciliation obligation is bound into the digest.
#[must_use]
pub fn pending_ledger_digest(entries: &[CompensationLedgerEntry]) -> Digest {
    let mut sorted = entries.iter().collect::<Vec<_>>();
    sorted.sort_by(|left, right| {
        (
            left.effect_sequence,
            left.ledger_sequence,
            left.ledger_entry_id.as_str(),
        )
            .cmp(&(
                right.effect_sequence,
                right.ledger_sequence,
                right.ledger_entry_id.as_str(),
            ))
    });
    let mut canonical = Canonical::new(b"bpmp-pending-compensation-ledger-v1");
    canonical.u64(sorted.len() as u64);
    for entry in sorted {
        canonical.string(&entry.tenant_id);
        canonical.string(&entry.instance_id);
        canonical.string(&entry.saga_ref);
        canonical.string(&entry.ledger_entry_id);
        canonical.u64(entry.effect_sequence);
        canonical.u64(entry.ledger_sequence);
        canonical.string(&entry.side_effect_type);
        canonical.string(&entry.target_system);
        canonical.string(&entry.handler_ref);
        canonical.string(&entry.opaque_operation_ref);
        canonical.string(&entry.idempotency_key);
        canonical.u8(match entry.status {
            CompensationStatus::Pending => 1,
            CompensationStatus::Compensated => 2,
            CompensationStatus::ReconciliationRequired => 3,
        });
        canonical.u64(entry.updated_at_epoch_ms);
    }
    canonical.finish()
}

#[must_use]
pub fn abort_request_digest(request: &AbortAndReconcileRequest) -> Digest {
    let mut canonical = Canonical::new(b"bpmp-abort-and-reconcile-request-v1");
    canonical.string(&request.tenant_id);
    canonical.string(&request.instance_id);
    canonical.string(&request.policy_id);
    canonical.u64(request.legal_deadline_epoch_ms);
    canonical.string(&request.key_scope);
    canonical.u64(request.key_epoch);
    canonical.bytes(&request.pending_ledger_digest);
    canonical.string(&request.reason_code);
    canonical.finish()
}

#[must_use]
pub fn approval_signing_payload(approval: &SignedApproval) -> Vec<u8> {
    let mut canonical = Canonical::new(b"bpmp-governance-approval-v1");
    canonical.bytes(&approval.request_digest);
    canonical.string(&approval.tenant_id);
    canonical.string(&approval.actor_id);
    canonical.string(&approval.capability);
    canonical.string(&approval.auth_assurance);
    canonical.u64(approval.approved_at_epoch_ms);
    canonical.u64(approval.expires_at_epoch_ms);
    canonical.string(&approval.key_id);
    canonical.into_bytes()
}

/// Validates proof and current ledger state and produces deterministic effects.
///
/// # Errors
///
/// Fails closed when request, ledger, policy, proof, signature, freshness, or
/// actor separation is invalid.
pub fn decide_abort_and_reconcile(
    request: &AbortAndReconcileRequest,
    pending_entries: &[CompensationLedgerEntry],
    proof: &DualControlProof,
    policy: &GovernancePolicy,
    evaluated_at_epoch_ms: u64,
) -> Result<AbortAndReconcileDecision, GovernanceError> {
    policy.validate()?;
    validate_request(request)?;
    let entry_count = u32::try_from(pending_entries.len()).unwrap_or(u32::MAX);
    if entry_count > policy.max_pending_ledger_entries {
        return Err(GovernanceError::PendingLedgerLimitExceeded {
            configured_limit: policy.max_pending_ledger_entries,
        });
    }
    validate_ledger(request, pending_entries)?;
    let ledger_digest = pending_ledger_digest(pending_entries);
    if ledger_digest != request.pending_ledger_digest {
        return Err(GovernanceError::StaleLedgerDigest);
    }
    let request_digest = abort_request_digest(request);
    let required = usize::from(policy.required_approver_count);
    if proof.approvers.len() < required {
        return Err(GovernanceError::InsufficientApprovers);
    }

    validate_approval(
        &proof.requester,
        request,
        request_digest,
        policy,
        evaluated_at_epoch_ms,
    )?;
    let mut actors = BTreeSet::from([proof.requester.actor_id.as_str()]);
    for approval in &proof.approvers {
        validate_approval(
            approval,
            request,
            request_digest,
            policy,
            evaluated_at_epoch_ms,
        )?;
        if !actors.insert(approval.actor_id.as_str()) {
            return Err(GovernanceError::ActorSeparationViolation);
        }
    }

    let mut sorted = pending_entries.iter().collect::<Vec<_>>();
    sorted.sort_by(|left, right| {
        (
            left.effect_sequence,
            left.ledger_sequence,
            &left.ledger_entry_id,
        )
            .cmp(&(
                right.effect_sequence,
                right.ledger_sequence,
                &right.ledger_entry_id,
            ))
    });
    let work_items = sorted
        .into_iter()
        .map(|entry| ReconciliationWorkItem {
            tenant_id: request.tenant_id.clone(),
            instance_id: request.instance_id.clone(),
            reconciliation_id: reconciliation_id(request_digest, &entry.ledger_entry_id),
            ledger_entry_id: entry.ledger_entry_id.clone(),
            side_effect_type: entry.side_effect_type.clone(),
            target_system: entry.target_system.clone(),
            handler_ref: entry.handler_ref.clone(),
            opaque_operation_ref: entry.opaque_operation_ref.clone(),
            deadline_epoch_ms: request.legal_deadline_epoch_ms,
        })
        .collect();

    Ok(AbortAndReconcileDecision {
        request_digest,
        ledger_digest,
        requester_audit: audit_ref(&proof.requester),
        approver_audits: proof.approvers.iter().map(audit_ref).collect(),
        work_items,
    })
}

fn validate_request(request: &AbortAndReconcileRequest) -> Result<(), GovernanceError> {
    if request.tenant_id.trim().is_empty()
        || request.instance_id.trim().is_empty()
        || request.policy_id.trim().is_empty()
        || request.key_scope.trim().is_empty()
        || request.reason_code.trim().is_empty()
        || request.legal_deadline_epoch_ms == 0
    {
        return Err(GovernanceError::InvalidRequest);
    }
    Ok(())
}

fn validate_ledger(
    request: &AbortAndReconcileRequest,
    entries: &[CompensationLedgerEntry],
) -> Result<(), GovernanceError> {
    let mut identities = BTreeSet::new();
    let mut order = BTreeSet::new();
    for entry in entries {
        if entry.tenant_id != request.tenant_id || entry.instance_id != request.instance_id {
            return Err(GovernanceError::LedgerScopeMismatch);
        }
        if entry.status != CompensationStatus::Pending {
            return Err(GovernanceError::LedgerEntryNotPending);
        }
        if entry.ledger_entry_id.trim().is_empty()
            || entry.side_effect_type.trim().is_empty()
            || entry.target_system.trim().is_empty()
            || entry.handler_ref.trim().is_empty()
            || entry.opaque_operation_ref.trim().is_empty()
            || entry.idempotency_key.trim().is_empty()
        {
            return Err(GovernanceError::MissingReconciliationMetadata);
        }
        if !identities.insert(entry.ledger_entry_id.as_str())
            || !order.insert((entry.effect_sequence, entry.ledger_sequence))
        {
            return Err(GovernanceError::DuplicateLedgerEntry);
        }
    }
    Ok(())
}

fn validate_approval(
    approval: &SignedApproval,
    request: &AbortAndReconcileRequest,
    request_digest: Digest,
    policy: &GovernancePolicy,
    evaluated_at_epoch_ms: u64,
) -> Result<(), GovernanceError> {
    if approval.request_digest != request_digest {
        return Err(GovernanceError::ProofDigestMismatch);
    }
    if approval.tenant_id != request.tenant_id {
        return Err(GovernanceError::ApprovalTenantMismatch);
    }
    if approval.capability != policy.abort_capability {
        return Err(GovernanceError::CapabilityDenied);
    }
    if !policy
        .accepted_auth_assurance
        .contains(&approval.auth_assurance)
    {
        return Err(GovernanceError::AuthenticationAssuranceDenied);
    }
    let lifetime = approval
        .expires_at_epoch_ms
        .checked_sub(approval.approved_at_epoch_ms)
        .ok_or(GovernanceError::ApprovalOutsideValidity)?;
    if lifetime > policy.max_approval_ttl_ms {
        return Err(GovernanceError::ApprovalLifetimeExceeded);
    }
    let age = evaluated_at_epoch_ms
        .checked_sub(approval.approved_at_epoch_ms)
        .ok_or(GovernanceError::ApprovalOutsideValidity)?;
    if evaluated_at_epoch_ms >= approval.expires_at_epoch_ms || age > policy.max_proof_age_ms {
        return Err(GovernanceError::ApprovalOutsideValidity);
    }
    let key = policy
        .approval_public_keys
        .get(&approval.key_id)
        .ok_or(GovernanceError::UnknownApprovalKey)
        .and_then(|bytes| {
            VerifyingKey::from_bytes(bytes).map_err(|_| GovernanceError::UnknownApprovalKey)
        })?;
    let signature = Signature::from_slice(&approval.signature)
        .map_err(|_| GovernanceError::InvalidApprovalSignature)?;
    key.verify_strict(&approval_signing_payload(approval), &signature)
        .map_err(|_| GovernanceError::InvalidApprovalSignature)
}

fn audit_ref(approval: &SignedApproval) -> GovernanceAuditRef {
    GovernanceAuditRef {
        actor_id: approval.actor_id.clone(),
        approved_at_epoch_ms: approval.approved_at_epoch_ms,
        key_id: approval.key_id.clone(),
    }
}

fn reconciliation_id(request_digest: Digest, ledger_entry_id: &str) -> String {
    let mut canonical = Canonical::new(b"bpmp-reconciliation-work-item-v1");
    canonical.bytes(&request_digest);
    canonical.string(ledger_entry_id);
    let digest = canonical.finish();
    digest.iter().fold(
        String::with_capacity(digest.len() * 2),
        |mut encoded, byte| {
            let _ = write!(encoded, "{byte:02x}");
            encoded
        },
    )
}

struct Canonical {
    bytes: Vec<u8>,
}

impl Canonical {
    fn new(domain: &[u8]) -> Self {
        let mut value = Self { bytes: Vec::new() };
        value.bytes(domain);
        value
    }

    fn string(&mut self, value: &str) {
        self.bytes(value.as_bytes());
    }

    fn bytes(&mut self, value: &[u8]) {
        self.u64(value.len() as u64);
        self.bytes.extend_from_slice(value);
    }

    fn u64(&mut self, value: u64) {
        self.bytes.extend_from_slice(&value.to_be_bytes());
    }

    fn u8(&mut self, value: u8) {
        self.bytes.push(value);
    }

    fn finish(self) -> Digest {
        Sha256::digest(self.bytes).into()
    }

    fn into_bytes(self) -> Vec<u8> {
        self.bytes
    }
}

#[cfg(test)]
mod tests {
    use ed25519_dalek::{Signer as _, SigningKey};

    use super::*;

    const NOW: u64 = 50_000;

    fn entry(id: &str, effect: u64) -> CompensationLedgerEntry {
        CompensationLedgerEntry {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-a".into(),
            saga_ref: "saga-a".into(),
            ledger_entry_id: id.into(),
            effect_sequence: effect,
            ledger_sequence: effect,
            side_effect_type: "payment".into(),
            target_system: "bank".into(),
            handler_ref: "refund-v1".into(),
            opaque_operation_ref: format!("opaque-{id}"),
            idempotency_key: format!("idem-{id}"),
            status: CompensationStatus::Pending,
            updated_at_epoch_ms: 40_000,
        }
    }

    fn policy(keys: &[(&str, &SigningKey)]) -> GovernancePolicy {
        GovernancePolicy {
            abort_capability: "tenant-policy.abort".into(),
            accepted_auth_assurance: BTreeSet::from(["aal-high".into()]),
            approval_public_keys: keys
                .iter()
                .map(|(id, key)| ((*id).to_owned(), key.verifying_key().to_bytes()))
                .collect(),
            required_approver_count: 1,
            max_proof_age_ms: 10_000,
            max_approval_ttl_ms: 20_000,
            max_pending_ledger_entries: 10,
        }
    }

    fn signed(actor: &str, key_id: &str, key: &SigningKey, digest: Digest) -> SignedApproval {
        let mut approval = SignedApproval {
            request_digest: digest,
            tenant_id: "tenant-a".into(),
            actor_id: actor.into(),
            capability: "tenant-policy.abort".into(),
            auth_assurance: "aal-high".into(),
            approved_at_epoch_ms: 45_000,
            expires_at_epoch_ms: 55_000,
            key_id: key_id.into(),
            signature: Vec::new(),
        };
        approval.signature = key
            .sign(&approval_signing_payload(&approval))
            .to_bytes()
            .to_vec();
        approval
    }

    fn request(entries: &[CompensationLedgerEntry]) -> AbortAndReconcileRequest {
        AbortAndReconcileRequest {
            tenant_id: "tenant-a".into(),
            instance_id: "instance-a".into(),
            policy_id: "policy-7".into(),
            legal_deadline_epoch_ms: 100_000,
            key_scope: "subject-key:42".into(),
            key_epoch: 9,
            pending_ledger_digest: pending_ledger_digest(entries),
            reason_code: "legal-deadline".into(),
        }
    }

    #[test]
    fn valid_dual_control_produces_one_deterministic_item_per_pending_effect() {
        let requester_key = SigningKey::from_bytes(&[7; 32]);
        let approver_key = SigningKey::from_bytes(&[8; 32]);
        let entries = vec![entry("second", 2), entry("first", 1)];
        let request = request(&entries);
        let digest = abort_request_digest(&request);
        let proof = DualControlProof {
            requester: signed("requester", "requester-key", &requester_key, digest),
            approvers: vec![signed("approver", "approver-key", &approver_key, digest)],
        };

        let decision = decide_abort_and_reconcile(
            &request,
            &entries,
            &proof,
            &policy(&[
                ("requester-key", &requester_key),
                ("approver-key", &approver_key),
            ]),
            NOW,
        )
        .unwrap();

        assert_eq!(decision.work_items.len(), 2);
        assert_eq!(decision.work_items[0].ledger_entry_id, "first");
        assert_ne!(
            decision.work_items[0].reconciliation_id,
            decision.work_items[1].reconciliation_id
        );
    }

    #[test]
    fn changed_ledger_invalidates_existing_proof() {
        let requester_key = SigningKey::from_bytes(&[7; 32]);
        let approver_key = SigningKey::from_bytes(&[8; 32]);
        let original_entries = vec![entry("first", 1)];
        let request = request(&original_entries);
        let digest = abort_request_digest(&request);
        let proof = DualControlProof {
            requester: signed("requester", "requester-key", &requester_key, digest),
            approvers: vec![signed("approver", "approver-key", &approver_key, digest)],
        };
        let changed_entries = vec![entry("first", 1), entry("second", 2)];

        assert_eq!(
            decide_abort_and_reconcile(
                &request,
                &changed_entries,
                &proof,
                &policy(&[
                    ("requester-key", &requester_key),
                    ("approver-key", &approver_key),
                ]),
                NOW,
            ),
            Err(GovernanceError::StaleLedgerDigest)
        );
    }

    #[test]
    fn requester_cannot_approve_own_request() {
        let key = SigningKey::from_bytes(&[7; 32]);
        let entries = vec![entry("first", 1)];
        let request = request(&entries);
        let digest = abort_request_digest(&request);
        let proof = DualControlProof {
            requester: signed("same-actor", "key", &key, digest),
            approvers: vec![signed("same-actor", "key", &key, digest)],
        };

        assert_eq!(
            decide_abort_and_reconcile(&request, &entries, &proof, &policy(&[("key", &key)]), NOW,),
            Err(GovernanceError::ActorSeparationViolation)
        );
    }

    #[test]
    fn missing_opaque_reference_fails_closed() {
        let requester_key = SigningKey::from_bytes(&[7; 32]);
        let approver_key = SigningKey::from_bytes(&[8; 32]);
        let mut entries = vec![entry("first", 1)];
        entries[0].opaque_operation_ref.clear();
        let request = request(&entries);
        let digest = abort_request_digest(&request);
        let proof = DualControlProof {
            requester: signed("requester", "requester-key", &requester_key, digest),
            approvers: vec![signed("approver", "approver-key", &approver_key, digest)],
        };

        assert_eq!(
            decide_abort_and_reconcile(
                &request,
                &entries,
                &proof,
                &policy(&[
                    ("requester-key", &requester_key),
                    ("approver-key", &approver_key),
                ]),
                NOW,
            ),
            Err(GovernanceError::MissingReconciliationMetadata)
        );
    }

    #[test]
    fn ledger_digest_is_independent_of_input_order() {
        let first = entry("first", 1);
        let second = entry("second", 2);
        assert_eq!(
            pending_ledger_digest(&[first.clone(), second.clone()]),
            pending_ledger_digest(&[second, first])
        );
    }
}
