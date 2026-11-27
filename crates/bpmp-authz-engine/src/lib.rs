//! Pure deterministic BPMP transition authorization.
//!
//! This crate intentionally has no I/O, async runtime, clock, randomness,
//! database, network client, or ambient mutable state. Callers must verify the
//! signed bundle and inject evaluation time plus current revocation floors.

use bpmp_authz_contracts::authorization::v1::{
    ActorAttribute, AttributeScopeOperator, AttributeScopePredicate, AttributeValueSource,
    AuthorizationPolicyBundle, AuthorizationPolicyEffect, AuthorizationPolicyGrant,
    AuthorizationResourceDefinition, AuthorizationResourceGrant,
};

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct AuthorizationInput<'a> {
    pub tenant_id: &'a str,
    pub actor_id: &'a str,
    pub roles: &'a [String],
    pub capabilities: &'a [String],
    pub actor_proof_revoke_epoch: u64,
    pub evaluated_at_epoch_ms: u64,
    pub workflow_type: &'a str,
    pub workflow_version: &'a str,
    pub active_node_id: &'a str,
    pub action: &'a str,
}

/// Input for authorization owned by a data or domain service.
/// `resource_branch_path` is ordered root-to-leaf and is supplied by the
/// authoritative resource owner, never by the browser.
#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ResourceAuthorizationInput<'a> {
    pub tenant_id: &'a str,
    pub actor_id: &'a str,
    pub roles: &'a [String],
    pub capabilities: &'a [String],
    pub actor_proof_revoke_epoch: u64,
    pub evaluated_at_epoch_ms: u64,
    pub owner_id: &'a str,
    pub resource_type: &'a str,
    pub action: &'a str,
    pub resource_id: &'a str,
    pub resource_owner_actor_id: Option<&'a str>,
    pub resource_branch_path: &'a [String],
    pub resource_queue_id: Option<&'a str>,
    pub actor_attributes: &'a [ActorAttribute],
    pub resource_attributes: &'a [ActorAttribute],
}

#[derive(Debug, Clone, Copy, Default, Eq, PartialEq)]
pub struct RevocationSnapshot {
    pub tenant_epoch: u64,
    pub actor_epoch: u64,
}

impl RevocationSnapshot {
    pub const fn required_epoch(self) -> u64 {
        if self.tenant_epoch > self.actor_epoch {
            self.tenant_epoch
        } else {
            self.actor_epoch
        }
    }
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum DecisionType {
    Allow,
    Deny,
}

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub enum DenyReason {
    BundleTenantMismatch,
    BundleNotYetValid,
    BundleExpired,
    ActorProofRevoked,
    InvalidPolicyEffect,
    NoMatchingGrant,
    ExplicitDeny,
}

impl DenyReason {
    pub const fn code(self) -> &'static str {
        match self {
            Self::BundleTenantMismatch => "BUNDLE_TENANT_MISMATCH",
            Self::BundleNotYetValid => "BUNDLE_NOT_YET_VALID",
            Self::BundleExpired => "BUNDLE_EXPIRED",
            Self::ActorProofRevoked => "ACTOR_PROOF_REVOKED",
            Self::InvalidPolicyEffect => "INVALID_POLICY_EFFECT",
            Self::NoMatchingGrant => "NO_MATCHING_GRANT",
            Self::ExplicitDeny => "EXPLICIT_DENY",
        }
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct AuthorizationDecision {
    pub decision: DecisionType,
    pub deny_reason: Option<DenyReason>,
    pub policy_version: String,
    pub bundle_sequence: u64,
    pub required_revoke_epoch: u64,
    pub matched_grant_ids: Vec<String>,
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct ResourceAuthorizationDecision {
    pub decision: DecisionType,
    pub deny_reason: Option<DenyReason>,
    pub policy_version: String,
    pub catalog_version: u64,
    pub bundle_sequence: u64,
    pub required_revoke_epoch: u64,
    pub matched_grant_ids: Vec<String>,
    pub readable_fields: Vec<String>,
    pub writable_fields: Vec<String>,
}

/// Evaluates a resource/action against an already verified signed bundle.
/// Empty branch and queue scope sets represent tenant scope. When both sets
/// are present they intersect; branch scope includes descendants only when
/// the grant explicitly enables that behavior. Explicit deny always wins.
pub fn evaluate_resource(
    bundle: &AuthorizationPolicyBundle,
    input: &ResourceAuthorizationInput<'_>,
    external_revocation: RevocationSnapshot,
) -> ResourceAuthorizationDecision {
    let required_revoke_epoch = required_revoke_epoch(bundle, input.actor_id, external_revocation);
    let mut decision = ResourceAuthorizationDecision {
        decision: DecisionType::Deny,
        deny_reason: None,
        policy_version: bundle.policy_version.clone(),
        catalog_version: bundle.catalog_version,
        bundle_sequence: bundle.bundle_sequence,
        required_revoke_epoch,
        matched_grant_ids: Vec::new(),
        readable_fields: Vec::new(),
        writable_fields: Vec::new(),
    };
    if bundle.tenant_id != input.tenant_id {
        decision.deny_reason = Some(DenyReason::BundleTenantMismatch);
        return decision;
    }
    if input.evaluated_at_epoch_ms < bundle.valid_from_epoch_ms {
        decision.deny_reason = Some(DenyReason::BundleNotYetValid);
        return decision;
    }
    if input.evaluated_at_epoch_ms >= bundle.expires_at_epoch_ms {
        decision.deny_reason = Some(DenyReason::BundleExpired);
        return decision;
    }
    if input.actor_proof_revoke_epoch < required_revoke_epoch {
        decision.deny_reason = Some(DenyReason::ActorProofRevoked);
        return decision;
    }
    let catalog_action =
        bundle
            .resource_catalog
            .iter()
            .any(|definition: &AuthorizationResourceDefinition| {
                definition.owner_id == input.owner_id
                    && selector_matches(&definition.resource_type, input.resource_type)
                    && selector_matches(&definition.action, input.action)
                    && input.capabilities.contains(&definition.capability)
            });
    if !catalog_action {
        decision.deny_reason = Some(DenyReason::NoMatchingGrant);
        return decision;
    }

    let mut allows: Vec<&AuthorizationResourceGrant> = Vec::new();
    let mut denies: Vec<&AuthorizationResourceGrant> = Vec::new();
    for grant in &bundle.resource_grants {
        if !resource_grant_matches(grant, input) {
            continue;
        }
        match AuthorizationPolicyEffect::try_from(grant.effect) {
            Ok(AuthorizationPolicyEffect::Allow) => allows.push(grant),
            Ok(AuthorizationPolicyEffect::Deny) => denies.push(grant),
            Ok(AuthorizationPolicyEffect::Unspecified) | Err(_) => {
                decision.deny_reason = Some(DenyReason::InvalidPolicyEffect);
                decision.matched_grant_ids.push(grant.grant_id.clone());
                return decision;
            }
        }
    }
    if !denies.is_empty() {
        decision.deny_reason = Some(DenyReason::ExplicitDeny);
        decision.matched_grant_ids = denies.iter().map(|grant| grant.grant_id.clone()).collect();
        return decision;
    }
    if allows.is_empty() {
        decision.deny_reason = Some(DenyReason::NoMatchingGrant);
        return decision;
    }

    // Multiple matching allows union. Each set is sorted/deduplicated so the
    // decision remains deterministic regardless of grant storage order.
    decision.decision = DecisionType::Allow;
    decision.matched_grant_ids = allows.iter().map(|grant| grant.grant_id.clone()).collect();
    decision.matched_grant_ids.sort();
    decision.readable_fields = allows
        .iter()
        .flat_map(|grant| grant.readable_fields.clone())
        .collect();
    decision.readable_fields.sort();
    decision.readable_fields.dedup();
    decision.writable_fields = allows
        .iter()
        .flat_map(|grant| grant.writable_fields.clone())
        .collect();
    decision.writable_fields.sort();
    decision.writable_fields.dedup();
    decision
}

fn resource_grant_matches(
    grant: &AuthorizationResourceGrant,
    input: &ResourceAuthorizationInput<'_>,
) -> bool {
    let narrower_scope = !grant.branch_scope_ids.is_empty()
        || !grant.queue_scope_ids.is_empty()
        || grant.own_scope
        || !grant.resource_scope_ids.is_empty()
        || !grant.attribute_predicates.is_empty();
    if grant.tenant_scope == narrower_scope {
        return false;
    }
    let subject_matches = grant.actor_ids.iter().any(|actor| actor == input.actor_id)
        || grant.roles.iter().any(|role| input.roles.contains(role));
    let capabilities_match = grant
        .required_capabilities
        .iter()
        .all(|required| input.capabilities.contains(required));
    let resource_matches = grant.owner_id == input.owner_id
        && selector_matches(&grant.resource_type, input.resource_type)
        && selector_matches(&grant.action, input.action);
    let branch_matches = if grant.branch_scope_ids.is_empty() {
        true
    } else if grant.branch_includes_descendants {
        grant
            .branch_scope_ids
            .iter()
            .any(|scope| input.resource_branch_path.contains(scope))
    } else {
        input
            .resource_branch_path
            .last()
            .is_some_and(|resource_branch| grant.branch_scope_ids.contains(resource_branch))
    };
    let queue_matches = grant.queue_scope_ids.is_empty()
        || input
            .resource_queue_id
            .is_some_and(|queue| grant.queue_scope_ids.iter().any(|scope| scope == queue));
    let own_matches = !grant.own_scope || input.resource_owner_actor_id == Some(input.actor_id);
    let resource_scope_matches = grant.resource_scope_ids.is_empty()
        || grant
            .resource_scope_ids
            .iter()
            .any(|scope| scope == input.resource_id);
    let attributes_match = attribute_predicates_match(
        &grant.attribute_predicates,
        input.actor_attributes,
        input.resource_attributes,
    );
    subject_matches
        && capabilities_match
        && resource_matches
        && branch_matches
        && queue_matches
        && own_matches
        && resource_scope_matches
        && attributes_match
}

fn attribute_predicates_match(
    predicates: &[AttributeScopePredicate],
    actor_attributes: &[ActorAttribute],
    resource_attributes: &[ActorAttribute],
) -> bool {
    predicates.iter().all(|predicate| {
        let Some(res_values) = find_attribute_values(resource_attributes, &predicate.resource_attribute)
        else {
            return false;
        };
        if res_values.is_empty() {
            return false;
        }
        let expected_values: &[String] = match AttributeValueSource::try_from(predicate.value_source) {
            Ok(AttributeValueSource::Literal) => &predicate.literal_values,
            Ok(AttributeValueSource::ActorAttribute) => {
                match find_attribute_values(actor_attributes, &predicate.actor_attribute_key) {
                    Some(values) => values,
                    None => return false,
                }
            }
            Ok(AttributeValueSource::Unspecified) | Err(_) => return false,
        };
        if expected_values.is_empty() {
            return false;
        }
        match AttributeScopeOperator::try_from(predicate.operator) {
            Ok(AttributeScopeOperator::Eq) => {
                res_values.len() == 1
                    && expected_values.len() == 1
                    && res_values[0] == expected_values[0]
            }
            Ok(AttributeScopeOperator::In) => res_values
                .iter()
                .all(|res| expected_values.iter().any(|exp| exp == res)),
            Ok(AttributeScopeOperator::ContainsAny) => res_values
                .iter()
                .any(|res| expected_values.iter().any(|exp| exp == res)),
            Ok(AttributeScopeOperator::Gte) => {
                parse_single_i64(res_values)
                    .zip(parse_single_i64(expected_values))
                    .is_some_and(|(lhs, rhs)| lhs >= rhs)
            }
            Ok(AttributeScopeOperator::Lte) => {
                parse_single_i64(res_values)
                    .zip(parse_single_i64(expected_values))
                    .is_some_and(|(lhs, rhs)| lhs <= rhs)
            }
            Ok(AttributeScopeOperator::Unspecified) | Err(_) => false,
        }
    })
}

fn find_attribute_values<'a>(attributes: &'a [ActorAttribute], key: &str) -> Option<&'a [String]> {
    if key.is_empty() {
        return None;
    }
    attributes
        .iter()
        .find(|attr| attr.key == key)
        .map(|attr| attr.values.as_slice())
}

fn parse_single_i64(values: &[String]) -> Option<i64> {
    if values.len() != 1 {
        return None;
    }
    values[0].parse::<i64>().ok()
}

impl AuthorizationDecision {
    fn deny(
        bundle: &AuthorizationPolicyBundle,
        reason: DenyReason,
        required_revoke_epoch: u64,
        matched_grant_ids: Vec<String>,
    ) -> Self {
        Self {
            decision: DecisionType::Deny,
            deny_reason: Some(reason),
            policy_version: bundle.policy_version.clone(),
            bundle_sequence: bundle.bundle_sequence,
            required_revoke_epoch,
            matched_grant_ids,
        }
    }
}

/// Evaluates one transition against an already verified policy bundle.
///
/// The function is total and fail-closed. Equal inputs always produce an equal
/// decision; no value is read from process or machine state.
pub fn evaluate(
    bundle: &AuthorizationPolicyBundle,
    input: &AuthorizationInput<'_>,
    external_revocation: RevocationSnapshot,
) -> AuthorizationDecision {
    let required_revoke_epoch = required_revoke_epoch(bundle, input.actor_id, external_revocation);
    if bundle.tenant_id != input.tenant_id {
        return AuthorizationDecision::deny(
            bundle,
            DenyReason::BundleTenantMismatch,
            required_revoke_epoch,
            Vec::new(),
        );
    }
    if input.evaluated_at_epoch_ms < bundle.valid_from_epoch_ms {
        return AuthorizationDecision::deny(
            bundle,
            DenyReason::BundleNotYetValid,
            required_revoke_epoch,
            Vec::new(),
        );
    }
    if input.evaluated_at_epoch_ms >= bundle.expires_at_epoch_ms {
        return AuthorizationDecision::deny(
            bundle,
            DenyReason::BundleExpired,
            required_revoke_epoch,
            Vec::new(),
        );
    }
    if input.actor_proof_revoke_epoch < required_revoke_epoch {
        return AuthorizationDecision::deny(
            bundle,
            DenyReason::ActorProofRevoked,
            required_revoke_epoch,
            Vec::new(),
        );
    }

    let mut allows = Vec::new();
    let mut denies = Vec::new();
    for grant in &bundle.grants {
        if !grant_matches(grant, input) {
            continue;
        }
        match AuthorizationPolicyEffect::try_from(grant.effect) {
            Ok(AuthorizationPolicyEffect::Allow) => allows.push(grant.grant_id.clone()),
            Ok(AuthorizationPolicyEffect::Deny) => denies.push(grant.grant_id.clone()),
            Ok(AuthorizationPolicyEffect::Unspecified) | Err(_) => {
                return AuthorizationDecision::deny(
                    bundle,
                    DenyReason::InvalidPolicyEffect,
                    required_revoke_epoch,
                    vec![grant.grant_id.clone()],
                );
            }
        }
    }

    if !denies.is_empty() {
        return AuthorizationDecision::deny(
            bundle,
            DenyReason::ExplicitDeny,
            required_revoke_epoch,
            denies,
        );
    }
    if allows.is_empty() {
        return AuthorizationDecision::deny(
            bundle,
            DenyReason::NoMatchingGrant,
            required_revoke_epoch,
            Vec::new(),
        );
    }
    AuthorizationDecision {
        decision: DecisionType::Allow,
        deny_reason: None,
        policy_version: bundle.policy_version.clone(),
        bundle_sequence: bundle.bundle_sequence,
        required_revoke_epoch,
        matched_grant_ids: allows,
    }
}

fn required_revoke_epoch(
    bundle: &AuthorizationPolicyBundle,
    actor_id: &str,
    external: RevocationSnapshot,
) -> u64 {
    let actor_bundle_epoch = bundle
        .actor_revoke_epochs
        .binary_search_by(|entry| entry.actor_id.as_str().cmp(actor_id))
        .ok()
        .map_or(0, |index| bundle.actor_revoke_epochs[index].revoke_epoch);
    bundle
        .revoke_epoch
        .max(actor_bundle_epoch)
        .max(external.required_epoch())
}

fn grant_matches(grant: &AuthorizationPolicyGrant, input: &AuthorizationInput<'_>) -> bool {
    subject_matches(grant, input)
        && grant
            .required_capabilities
            .iter()
            .all(|required| input.capabilities.contains(required))
        && selector_matches(&grant.workflow_type, input.workflow_type)
        && selector_matches(&grant.workflow_version, input.workflow_version)
        && selector_matches(&grant.active_node_id, input.active_node_id)
        && selector_matches(&grant.action, input.action)
}

fn subject_matches(grant: &AuthorizationPolicyGrant, input: &AuthorizationInput<'_>) -> bool {
    if grant.actor_ids.is_empty() && grant.roles.is_empty() {
        return true;
    }
    grant.actor_ids.iter().any(|actor| actor == input.actor_id)
        || grant.roles.iter().any(|role| input.roles.contains(role))
}

fn selector_matches(selector: &str, actual: &str) -> bool {
    selector == "*" || selector == actual
}

#[cfg(test)]
mod tests {
    use bpmp_authz_contracts::authorization::v1::{
        ActorRevokeEpoch, AuthorizationPolicyBundle, AuthorizationPolicyEffect,
        AuthorizationPolicyGrant,
    };

    use super::*;
    use proptest::prelude::*;

    fn grant(id: &str, effect: AuthorizationPolicyEffect) -> AuthorizationPolicyGrant {
        AuthorizationPolicyGrant {
            grant_id: id.into(),
            actor_ids: vec!["actor-1".into()],
            roles: Vec::new(),
            required_capabilities: vec!["workflow.start".into()],
            workflow_type: "order".into(),
            workflow_version: "1".into(),
            active_node_id: "start".into(),
            action: "START".into(),
            effect: effect.into(),
            priority: 0,
        }
    }

    fn bundle(grants: Vec<AuthorizationPolicyGrant>) -> AuthorizationPolicyBundle {
        AuthorizationPolicyBundle {
            schema_version: 1,
            tenant_id: "tenant-a".into(),
            bundle_sequence: 7,
            policy_version: "policy-v7".into(),
            revoke_epoch: 3,
            valid_from_epoch_ms: 100,
            expires_at_epoch_ms: 1_000,
            grants,
            catalog_version: 0,
            resource_grants: Vec::new(),
            resource_catalog: Vec::new(),
            actor_revoke_epochs: vec![ActorRevokeEpoch {
                actor_id: "actor-1".into(),
                revoke_epoch: 4,
            }],
            signing_key_id: "key-1".into(),
            content_hash: Vec::new(),
            signature: Vec::new(),
        }
    }

    fn input(proof_epoch: u64) -> AuthorizationInput<'static> {
        AuthorizationInput {
            tenant_id: "tenant-a",
            actor_id: "actor-1",
            roles: &[],
            capabilities: &[],
            actor_proof_revoke_epoch: proof_epoch,
            evaluated_at_epoch_ms: 500,
            workflow_type: "order",
            workflow_version: "1",
            active_node_id: "start",
            action: "START",
        }
    }

    #[test]
    fn equal_inputs_produce_equal_decisions() {
        let mut request = input(4);
        let capabilities = ["workflow.start".to_owned()];
        request.capabilities = &capabilities;
        let policy = bundle(vec![grant("allow-start", AuthorizationPolicyEffect::Allow)]);
        assert_eq!(
            evaluate(&policy, &request, RevocationSnapshot::default()),
            evaluate(&policy, &request, RevocationSnapshot::default())
        );
        assert_eq!(
            evaluate(&policy, &request, RevocationSnapshot::default()).decision,
            DecisionType::Allow
        );
    }

    #[test]
    fn explicit_deny_overrides_allow() {
        let mut request = input(4);
        let capabilities = ["workflow.start".to_owned()];
        request.capabilities = &capabilities;
        let policy = bundle(vec![
            grant("allow-start", AuthorizationPolicyEffect::Allow),
            grant("deny-start", AuthorizationPolicyEffect::Deny),
        ]);
        let decision = evaluate(&policy, &request, RevocationSnapshot::default());
        assert_eq!(decision.decision, DecisionType::Deny);
        assert_eq!(decision.deny_reason, Some(DenyReason::ExplicitDeny));
    }

    #[test]
    fn stale_actor_proof_fails_closed() {
        let decision = evaluate(
            &bundle(Vec::new()),
            &input(3),
            RevocationSnapshot::default(),
        );
        assert_eq!(decision.deny_reason, Some(DenyReason::ActorProofRevoked));
        assert_eq!(decision.required_revoke_epoch, 4);
    }

    #[test]
    fn external_revoke_floor_takes_precedence() {
        let decision = evaluate(
            &bundle(Vec::new()),
            &input(4),
            RevocationSnapshot {
                tenant_epoch: 8,
                actor_epoch: 0,
            },
        );
        assert_eq!(decision.deny_reason, Some(DenyReason::ActorProofRevoked));
        assert_eq!(decision.required_revoke_epoch, 8);
    }

    proptest! {
        #[test]
        fn evaluation_is_replay_deterministic(
            proof_epoch in 0_u64..20,
            evaluated_at in 0_u64..2_000,
            tenant_floor in 0_u64..20,
            actor_floor in 0_u64..20,
        ) {
            let capabilities = ["workflow.start".to_owned()];
            let mut request = input(proof_epoch);
            request.capabilities = &capabilities;
            request.evaluated_at_epoch_ms = evaluated_at;
            let revocation = RevocationSnapshot {
                tenant_epoch: tenant_floor,
                actor_epoch: actor_floor,
            };
            let policy = bundle(vec![grant("allow-start", AuthorizationPolicyEffect::Allow)]);
            prop_assert_eq!(
                evaluate(&policy, &request, revocation),
                evaluate(&policy, &request, revocation),
            );
        }
    }

    #[test]
    fn production_manifest_has_no_ambient_io_or_runtime_dependency() {
        let manifest = include_str!("../Cargo.toml");
        for forbidden in ["tokio", "sqlx", "reqwest", "chrono", "rand"] {
            assert!(
                !manifest
                    .lines()
                    .any(|line| line.trim_start().starts_with(forbidden)),
                "pure evaluator must not depend on {forbidden}"
            );
        }
    }

    fn attr(key: &str, values: &[&str]) -> ActorAttribute {
        ActorAttribute {
            key: key.into(),
            values: values.iter().map(|v| (*v).into()).collect(),
        }
    }

    fn resource_bundle(grants: Vec<AuthorizationResourceGrant>) -> AuthorizationPolicyBundle {
        AuthorizationPolicyBundle {
            schema_version: 1,
            tenant_id: "tenant-a".into(),
            bundle_sequence: 10,
            policy_version: "policy-v10".into(),
            revoke_epoch: 1,
            valid_from_epoch_ms: 100,
            expires_at_epoch_ms: 1_000,
            grants: Vec::new(),
            actor_revoke_epochs: Vec::new(),
            signing_key_id: "key-1".into(),
            content_hash: Vec::new(),
            signature: Vec::new(),
            catalog_version: 1,
            resource_grants: grants,
            resource_catalog: vec![AuthorizationResourceDefinition {
                owner_id: "projection-service".into(),
                manifest_version: "1.0.0".into(),
                manifest_digest: vec![1; 32],
                resource_type: "workflow_instance".into(),
                action: "read".into(),
                capability: "workflow.instance.read".into(),
                supported_scope_types: vec!["branch".into(), "attribute".into()],
                readable_fields: vec!["id".into(), "status".into(), "amount".into()],
                writable_fields: Vec::new(),
                supported_attribute_scopes: Vec::new(),
            }],
        }
    }

    #[test]
    fn dynamic_attribute_scope_supports_multi_workflow_predicates() {
        let grant = AuthorizationResourceGrant {
            grant_id: "credit-officer-scope".into(),
            actor_ids: Vec::new(),
            roles: vec!["credit_officer".into()],
            required_capabilities: vec!["workflow.instance.read".into()],
            resource_type: "workflow_instance".into(),
            action: "read".into(),
            effect: AuthorizationPolicyEffect::Allow.into(),
            priority: 0,
            branch_scope_ids: Vec::new(),
            branch_includes_descendants: false,
            queue_scope_ids: Vec::new(),
            readable_fields: vec!["id".into(), "amount".into()],
            writable_fields: Vec::new(),
            own_scope: false,
            resource_scope_ids: Vec::new(),
            owner_id: "projection-service".into(),
            tenant_scope: false,
            masked_fields: Vec::new(),
            attribute_predicates: vec![
                AttributeScopePredicate {
                    resource_attribute: "workflow_type".into(),
                    operator: AttributeScopeOperator::Eq.into(),
                    value_source: AttributeValueSource::Literal.into(),
                    literal_values: vec!["loan_approval".into()],
                    actor_attribute_key: String::new(),
                },
                AttributeScopePredicate {
                    resource_attribute: "region".into(),
                    operator: AttributeScopeOperator::In.into(),
                    value_source: AttributeValueSource::ActorAttribute.into(),
                    literal_values: Vec::new(),
                    actor_attribute_key: "regions".into(),
                },
                AttributeScopePredicate {
                    resource_attribute: "amount".into(),
                    operator: AttributeScopeOperator::Lte.into(),
                    value_source: AttributeValueSource::ActorAttribute.into(),
                    literal_values: Vec::new(),
                    actor_attribute_key: "loan.approval_limit".into(),
                },
            ],
        };
        let policy = resource_bundle(vec![grant]);
        let roles = ["credit_officer".to_owned()];
        let caps = ["workflow.instance.read".to_owned()];
        let actor_attrs = [
            attr("loan.approval_limit", &["50000000"]),
            attr("regions", &["HN", "DN"]),
        ];
        let allowed_resource_attrs = [
            attr("workflow_type", &["loan_approval"]),
            attr("region", &["HN"]),
            attr("amount", &["30000000"]),
        ];
        let base_input = ResourceAuthorizationInput {
            tenant_id: "tenant-a",
            actor_id: "actor-1",
            roles: &roles,
            capabilities: &caps,
            actor_proof_revoke_epoch: 1,
            evaluated_at_epoch_ms: 500,
            owner_id: "projection-service",
            resource_type: "workflow_instance",
            action: "read",
            resource_id: "wf-1",
            resource_owner_actor_id: None,
            resource_branch_path: &[],
            resource_queue_id: None,
            actor_attributes: &actor_attrs,
            resource_attributes: &allowed_resource_attrs,
        };

        // 1. All predicates satisfied -> Allow
        let allow_decision = evaluate_resource(&policy, &base_input, RevocationSnapshot::default());
        assert_eq!(allow_decision.decision, DecisionType::Allow);
        assert_eq!(allow_decision.readable_fields, ["amount", "id"]);

        // 2. Exceeds approval_limit -> Deny
        let over_limit_attrs = [
            attr("workflow_type", &["loan_approval"]),
            attr("region", &["HN"]),
            attr("amount", &["80000000"]),
        ];
        let mut over_limit_input = base_input.clone();
        over_limit_input.resource_attributes = &over_limit_attrs;
        let deny_limit = evaluate_resource(&policy, &over_limit_input, RevocationSnapshot::default());
        assert_eq!(deny_limit.decision, DecisionType::Deny);
        assert_eq!(deny_limit.deny_reason, Some(DenyReason::NoMatchingGrant));

        // 3. Missing actor attribute -> Fail-closed Deny
        let mut missing_actor_attr = base_input.clone();
        missing_actor_attr.actor_attributes = &[];
        let deny_missing = evaluate_resource(&policy, &missing_actor_attr, RevocationSnapshot::default());
        assert_eq!(deny_missing.decision, DecisionType::Deny);

        // 4. Non-numeric amount on LTE -> Fail-closed Deny
        let bad_amount_attrs = [
            attr("workflow_type", &["loan_approval"]),
            attr("region", &["HN"]),
            attr("amount", &["not-a-number"]),
        ];
        let mut bad_amount_input = base_input;
        bad_amount_input.resource_attributes = &bad_amount_attrs;
        let deny_bad = evaluate_resource(&policy, &bad_amount_input, RevocationSnapshot::default());
        assert_eq!(deny_bad.decision, DecisionType::Deny);
    }

    #[test]
    fn explicit_deny_with_attribute_predicate_overrides_allow() {
        let allow_grant = AuthorizationResourceGrant {
            grant_id: "allow-region".into(),
            actor_ids: vec!["actor-1".into()],
            roles: Vec::new(),
            required_capabilities: vec!["workflow.instance.read".into()],
            resource_type: "workflow_instance".into(),
            action: "read".into(),
            effect: AuthorizationPolicyEffect::Allow.into(),
            priority: 0,
            branch_scope_ids: Vec::new(),
            branch_includes_descendants: false,
            queue_scope_ids: Vec::new(),
            readable_fields: vec!["id".into()],
            writable_fields: Vec::new(),
            own_scope: false,
            resource_scope_ids: Vec::new(),
            owner_id: "projection-service".into(),
            tenant_scope: false,
            masked_fields: Vec::new(),
            attribute_predicates: vec![AttributeScopePredicate {
                resource_attribute: "region".into(),
                operator: AttributeScopeOperator::In.into(),
                value_source: AttributeValueSource::ActorAttribute.into(),
                literal_values: Vec::new(),
                actor_attribute_key: "regions".into(),
            }],
        };
        let deny_grant = AuthorizationResourceGrant {
            grant_id: "deny-vip".into(),
            effect: AuthorizationPolicyEffect::Deny.into(),
            attribute_predicates: vec![AttributeScopePredicate {
                resource_attribute: "risk_tier".into(),
                operator: AttributeScopeOperator::Eq.into(),
                value_source: AttributeValueSource::Literal.into(),
                literal_values: vec!["RESTRICTED".into()],
                actor_attribute_key: String::new(),
            }],
            ..allow_grant.clone()
        };
        let policy = resource_bundle(vec![allow_grant, deny_grant]);
        let caps = ["workflow.instance.read".to_owned()];
        let actor_attrs = [attr("regions", &["HN"])];
        let resource_attrs = [attr("region", &["HN"]), attr("risk_tier", &["RESTRICTED"])];
        let input = ResourceAuthorizationInput {
            tenant_id: "tenant-a",
            actor_id: "actor-1",
            roles: &[],
            capabilities: &caps,
            actor_proof_revoke_epoch: 1,
            evaluated_at_epoch_ms: 500,
            owner_id: "projection-service",
            resource_type: "workflow_instance",
            action: "read",
            resource_id: "wf-vip",
            resource_owner_actor_id: None,
            resource_branch_path: &[],
            resource_queue_id: None,
            actor_attributes: &actor_attrs,
            resource_attributes: &resource_attrs,
        };
        let decision = evaluate_resource(&policy, &input, RevocationSnapshot::default());
        assert_eq!(decision.decision, DecisionType::Deny);
        assert_eq!(decision.deny_reason, Some(DenyReason::ExplicitDeny));
        assert_eq!(decision.matched_grant_ids, ["deny-vip"]);
    }
}
