//! Temporal gate evaluator (EC-1).
//!
//! ## Design
//! Evaluated BEFORE the compiled predicate cache — because `env.now()` changes
//! per request and must never be baked into cached predicates.
//!
//! This evaluator is:
//! - Pure in-memory arithmetic (no DB round-trip after initial policy load)
//! - Non-cacheable result (by design — each request evaluates fresh)
//! - Short-circuit: deny early, before touching ABAC/ReBAC

use authz_core::{ids::PermissionId, models::filter::TemporalPolicy, AuthzError};
use chrono::{Datelike, Timelike};
use ipnet::IpNet;
use std::net::IpAddr;
use std::str::FromStr;
use tracing::{debug, instrument};

use crate::cache::policy_bundle::PolicyBundle;
use crate::context::EnvContext;

/// Result of a temporal gate evaluation.
#[derive(Debug, Clone)]
pub struct TemporalResult {
    pub allowed: bool,
    pub reason: Option<String>,
}

impl TemporalResult {
    pub fn allowed() -> Self {
        Self {
            allowed: true,
            reason: None,
        }
    }

    pub fn denied(reason: impl Into<String>) -> Self {
        Self {
            allowed: false,
            reason: Some(reason.into()),
        }
    }
}

/// Evaluates temporal gate policies for a given permission.
///
/// All temporal policies for the permission are evaluated in order.
/// Any denial is immediately returned (no partial allow).
#[instrument(skip_all, fields(permission_id = %permission_id))]
pub async fn evaluate_temporal_gate(
    permission_id: PermissionId,
    policies: &[TemporalPolicy],
    env: &EnvContext,
) -> Result<TemporalResult, AuthzError> {
    if policies.is_empty() {
        debug!("No temporal policies — passing gate");
        return Ok(TemporalResult::allowed());
    }

    for policy in policies {
        if !policy.is_active {
            continue;
        }

        let result = evaluate_single_policy(policy, env)?;
        if !result.allowed {
            return Ok(result);
        }
    }

    Ok(TemporalResult::allowed())
}

/// Bundle-aware temporal gate that uses the `TemporalIntervalTree` inside
/// the `PolicyBundle` as an O(log N) pre-filter before running the full
/// per-policy day / timezone / CIDR check.
///
/// Falls back to `evaluate_temporal_gate` semantics when the bundle has
/// no entry for `permission_id` (loader/control-plane has not warmed it yet).
#[instrument(skip_all, fields(permission_id = %permission_id))]
pub async fn evaluate_temporal_gate_with_bundle(
    permission_id: PermissionId,
    bundle: &PolicyBundle,
    env: &EnvContext,
) -> Result<TemporalResult, AuthzError> {
    let perm_key = permission_id.to_string();
    let policies = match bundle.temporal_policies.get(&perm_key) {
        Some(p) if !p.is_empty() => p.as_slice(),
        _ => return Ok(TemporalResult::allowed()),
    };

    // O(log N) pre-filter: if the current time-of-day overlaps no window,
    // we can short-circuit without iterating policies.
    let now_seconds = env.request_time.num_seconds_from_midnight() as i64;
    if !bundle.has_time_window(&perm_key, now_seconds) {
        return Ok(TemporalResult::denied(
            "Current time-of-day is outside every configured window for this permission",
        ));
    }

    evaluate_temporal_gate(permission_id, policies, env).await
}

/// Evaluates a single temporal policy against the current environment.
fn evaluate_single_policy(
    policy: &TemporalPolicy,
    env: &EnvContext,
) -> Result<TemporalResult, AuthzError> {
    // Parse the timezone from the policy
    let tz: chrono_tz::Tz = policy.timezone.parse().map_err(|_| {
        AuthzError::Internal(format!(
            "Invalid timezone in temporal policy: {}",
            policy.timezone
        ))
    })?;

    let now_in_tz = env.request_time.with_timezone(&tz);
    let weekday_iso = now_in_tz.weekday().number_from_monday() as u8; // 1=Mon, 7=Sun
    let current_time = now_in_tz.time();

    // Check day of week
    if !policy.allowed_days.contains(&weekday_iso) {
        return Ok(TemporalResult::denied(format!(
            "Not allowed on day {} ({}). Allowed days: {:?}",
            weekday_iso,
            now_in_tz.weekday(),
            policy.allowed_days
        )));
    }

    // Check time range
    if current_time < policy.allowed_from || current_time > policy.allowed_until {
        return Ok(TemporalResult::denied(format!(
            "Outside allowed hours: {} not in {}–{} ({})",
            current_time.format("%H:%M"),
            policy.allowed_from.format("%H:%M"),
            policy.allowed_until.format("%H:%M"),
            policy.timezone
        )));
    }

    // Check IP CIDR (if configured)
    if let Some(allowed_cidrs) = &policy.allowed_cidr {
        if !allowed_cidrs.is_empty() {
            match env.client_ip {
                None => {
                    return Ok(TemporalResult::denied(
                        "IP whitelist is configured but no client IP available in request",
                    ));
                }
                Some(client_ip) => {
                    if !ip_in_cidr_list(client_ip, allowed_cidrs) {
                        return Ok(TemporalResult::denied(format!(
                            "Client IP {} is not in the allowed CIDR list",
                            client_ip
                        )));
                    }
                }
            }
        }
    }

    // Shift check is handled externally (EC-4 JIT fetch) — flagged in policy
    // The engine caller is responsible for checking shift_table_ref if require_shift is true

    Ok(TemporalResult::allowed())
}

/// Checks if an IP address falls within any of the given CIDR ranges.
fn ip_in_cidr_list(ip: IpAddr, cidrs: &[String]) -> bool {
    for cidr_str in cidrs {
        if let Ok(network) = IpNet::from_str(cidr_str) {
            if network.contains(&ip) {
                return true;
            }
        }
    }
    false
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::{NaiveTime, TimeZone, Utc};

    fn make_env(hour: u32, minute: u32, weekday_offset_days: i64) -> EnvContext {
        // Monday 2024-01-08 at hour:minute UTC (Vietnam is UTC+7)
        // Weekday 1 = Monday in ISO numbering
        let base = Utc.with_ymd_and_hms(2024, 1, 8, 0, 0, 0).unwrap(); // Monday
        let time = base
            + chrono::Duration::hours(hour as i64 - 7) // convert local to UTC
            + chrono::Duration::minutes(minute as i64)
            + chrono::Duration::days(weekday_offset_days);

        EnvContext {
            request_time: time,
            client_ip: None,
        }
    }

    fn make_policy(from: &str, until: &str, days: Vec<u8>) -> TemporalPolicy {
        TemporalPolicy {
            id: authz_core::ids::TemporalPolicyId::new(),
            permission_id: authz_core::ids::PermissionId::new(),
            name: "test".to_owned(),
            allowed_days: days,
            allowed_from: NaiveTime::parse_from_str(from, "%H:%M").unwrap(),
            allowed_until: NaiveTime::parse_from_str(until, "%H:%M").unwrap(),
            timezone: "Asia/Ho_Chi_Minh".to_owned(),
            allowed_cidr: None,
            require_shift: false,
            shift_table_ref: None,
            is_active: true,
            metadata: authz_core::models::metadata::EntityMetadata::from_persistence(
                0,
                false,
                None,
                None,
                Utc.with_ymd_and_hms(2024, 1, 1, 0, 0, 0).unwrap(),
                None,
                Utc.with_ymd_and_hms(2024, 1, 1, 0, 0, 0).unwrap(),
                None,
            ),
        }
    }

    #[tokio::test]
    async fn test_allowed_during_working_hours() {
        let env = make_env(10, 0, 0); // Monday 10:00 Vietnam time
        let policy = make_policy("08:00", "17:30", vec![1, 2, 3, 4, 5]);
        let result = evaluate_temporal_gate(authz_core::ids::PermissionId::new(), &[policy], &env)
            .await
            .unwrap();
        assert!(result.allowed, "Should be allowed during working hours");
    }

    #[tokio::test]
    async fn test_denied_outside_working_hours() {
        let env = make_env(20, 0, 0); // Monday 20:00 Vietnam time
        let policy = make_policy("08:00", "17:30", vec![1, 2, 3, 4, 5]);
        let result = evaluate_temporal_gate(authz_core::ids::PermissionId::new(), &[policy], &env)
            .await
            .unwrap();
        assert!(!result.allowed, "Should be denied outside working hours");
        assert!(result.reason.unwrap().contains("Outside allowed hours"));
    }

    #[tokio::test]
    async fn test_denied_on_weekend() {
        let env = make_env(10, 0, 5); // Saturday (Monday + 5 days)
        let policy = make_policy("08:00", "17:30", vec![1, 2, 3, 4, 5]);
        let result = evaluate_temporal_gate(authz_core::ids::PermissionId::new(), &[policy], &env)
            .await
            .unwrap();
        assert!(!result.allowed, "Should be denied on weekend");
    }

    #[tokio::test]
    async fn test_no_policies_passes() {
        let env = make_env(10, 0, 0);
        let result = evaluate_temporal_gate(authz_core::ids::PermissionId::new(), &[], &env)
            .await
            .unwrap();
        assert!(result.allowed, "No policies should always pass");
    }
}
