//! ABAC (Attribute-Based Access Control) AST evaluator.
//!
//! ## Design
//! - Pure functions: `evaluate_node` is deterministic and side-effect free.
//! - Full tracing: every node produces an `AstNodeTrace` for the Explain API (G7).
//! - No panics: all error paths return `Result<_, AuthzError>`.
//! - No blocking: all JIT attribute fetches are async.
//!
//! The evaluator walks the `ConditionNode` AST recursively, resolving each
//! `ValueSource` to a concrete JSON value, then applying the comparison operator.

use authz_core::{
    models::{
        audit::AstNodeTrace,
        policy::{ComparisonOperator, ConditionNode, EnvKind, LeafCondition, ValueSource},
    },
    AuthzError,
};
use serde_json::Value as JsonValue;
use tracing::instrument;

use crate::context::AuthzContext;

/// Result of evaluating the ABAC layer.
#[derive(Debug)]
pub struct AbacResult {
    pub allowed: bool,
    pub trace: AstNodeTrace,
}

/// Evaluates the ABAC condition AST against the given context.
///
/// Returns a full recursive trace for the G7 Explain API.
/// This function is the hot path — optimized for minimal allocations.
#[instrument(skip_all, fields(has_relation = ctx.resource.attributes.is_object()))]
pub async fn evaluate_abac(
    node: &ConditionNode,
    ctx: &AuthzContext,
    jit_fetcher: &dyn JitAttributeFetcher,
) -> Result<AbacResult, AuthzError> {
    let trace = evaluate_node(node, ctx, jit_fetcher).await?;
    let allowed = trace.result;
    Ok(AbacResult { allowed, trace })
}

/// Trait for fetching external attributes JIT (EC-4).
/// Allows injecting a mock in unit tests.
#[async_trait::async_trait]
pub trait JitAttributeFetcher: Send + Sync {
    async fn fetch(
        &self,
        source: &str,
        user_id: &str,
        key: &str,
        tenant_id: &str,
    ) -> Result<JsonValue, AuthzError>;
}

/// Evaluates a single AST node, recursively for AND/OR.
///
/// ## Short-circuit evaluation
/// - AND: returns false as soon as one child is false (saves processing)
/// - OR: returns true as soon as one child is true
async fn evaluate_node(
    node: &ConditionNode,
    ctx: &AuthzContext,
    jit: &dyn JitAttributeFetcher,
) -> Result<AstNodeTrace, AuthzError> {
    match node {
        ConditionNode::And { conditions } => evaluate_and(conditions, ctx, jit).await,
        ConditionNode::Or { conditions } => evaluate_or(conditions, ctx, jit).await,
        ConditionNode::Leaf(leaf) => evaluate_leaf(leaf, ctx, jit).await,
    }
}

async fn evaluate_and(
    conditions: &[ConditionNode],
    ctx: &AuthzContext,
    jit: &dyn JitAttributeFetcher,
) -> Result<AstNodeTrace, AuthzError> {
    let mut children = Vec::with_capacity(conditions.len());
    let mut overall = true;

    for cond in conditions {
        let child_trace = Box::pin(evaluate_node(cond, ctx, jit)).await?;
        if !child_trace.result {
            overall = false;
            children.push(child_trace);
            // Short-circuit: one false makes AND false — but still collect the trace
            break;
        }
        children.push(child_trace);
    }

    Ok(AstNodeTrace {
        node: "AND".to_owned(),
        result: overall,
        reason: if !overall {
            Some("One or more AND conditions failed".to_owned())
        } else {
            None
        },
        left_value: None,
        right_value: None,
        children,
    })
}

async fn evaluate_or(
    conditions: &[ConditionNode],
    ctx: &AuthzContext,
    jit: &dyn JitAttributeFetcher,
) -> Result<AstNodeTrace, AuthzError> {
    let mut children = Vec::with_capacity(conditions.len());
    let mut overall = false;

    for cond in conditions {
        let child_trace = Box::pin(evaluate_node(cond, ctx, jit)).await?;
        if child_trace.result {
            overall = true;
            children.push(child_trace);
            // Short-circuit: one true makes OR true
            break;
        }
        children.push(child_trace);
    }

    Ok(AstNodeTrace {
        node: "OR".to_owned(),
        result: overall,
        reason: if !overall {
            Some("All OR conditions failed".to_owned())
        } else {
            None
        },
        left_value: None,
        right_value: None,
        children: vec![],
    })
}

async fn evaluate_leaf(
    leaf: &LeafCondition,
    ctx: &AuthzContext,
    jit: &dyn JitAttributeFetcher,
) -> Result<AstNodeTrace, AuthzError> {
    let left_val = resolve_value(&leaf.left, ctx, jit).await?;
    let right_val = resolve_value(&leaf.right, ctx, jit).await?;

    let (result, reason) = apply_operator(&leaf.op, &left_val, &right_val);

    let node_desc = format!(
        "{} {:?} {}",
        describe_source(&leaf.left),
        leaf.op,
        describe_source(&leaf.right)
    );

    Ok(AstNodeTrace {
        node: node_desc,
        result,
        reason: Some(reason),
        left_value: Some(left_val),
        right_value: Some(right_val),
        children: vec![],
    })
}

/// Resolves a `ValueSource` to a concrete JSON value.
async fn resolve_value(
    source: &ValueSource,
    ctx: &AuthzContext,
    jit: &dyn JitAttributeFetcher,
) -> Result<JsonValue, AuthzError> {
    match source {
        ValueSource::UserAttr { key } => Ok(ctx.user_attr(key).cloned().unwrap_or(JsonValue::Null)),

        ValueSource::ResourceField { key } => {
            Ok(ctx.resource_attr(key).cloned().unwrap_or(JsonValue::Null))
        }

        ValueSource::Literal { value } => Ok(value.clone()),

        ValueSource::Env { kind } => Ok(match kind {
            EnvKind::Now => JsonValue::String(ctx.env.request_time.to_rfc3339()),
            EnvKind::CurrentDate => {
                JsonValue::String(ctx.env.request_time.format("%Y-%m-%d").to_string())
            }
            EnvKind::RequestIp => ctx
                .env
                .client_ip
                .map(|ip| JsonValue::String(ip.to_string()))
                .unwrap_or(JsonValue::Null),
        }),

        ValueSource::ExternalAttr { source, key } => {
            // EC-4: JIT fetch from external service
            let user_id_str = ctx.user_id.to_string();
            let tenant_id_str = ctx.tenant_id.to_string();
            jit.fetch(source, &user_id_str, key, &tenant_id_str).await
        }

        ValueSource::Relation { .. } => {
            // Relation nodes are handled by the ReBAC engine, not here.
            // The pipeline pre-checks relation nodes before reaching ABAC.
            // Returning Null here is safe — the `exists` operator handles it.
            Ok(JsonValue::Null)
        }
    }
}

/// Applies a comparison operator to two resolved values.
///
/// Returns `(result: bool, reason: String)` for the trace.
fn apply_operator(op: &ComparisonOperator, left: &JsonValue, right: &JsonValue) -> (bool, String) {
    match op {
        ComparisonOperator::Eq => {
            let result = left == right;
            let reason = if result {
                format!("{left} == {right}")
            } else {
                format!("{left} != {right}")
            };
            (result, reason)
        }

        ComparisonOperator::Neq => {
            let result = left != right;
            (result, format!("{left} != {right}: {result}"))
        }

        ComparisonOperator::In => {
            let result = match right {
                JsonValue::Array(arr) => arr.contains(left),
                _ => false,
            };
            let reason = if result {
                format!("{left} in {right}")
            } else {
                format!("{left} not in {right}")
            };
            (result, reason)
        }

        ComparisonOperator::NotIn => {
            let result = match right {
                JsonValue::Array(arr) => !arr.contains(left),
                _ => true,
            };
            (result, format!("{left} not_in {right}: {result}"))
        }

        ComparisonOperator::Gte => {
            let result = compare_numeric(left, right)
                .map(|o| o >= 0)
                .unwrap_or(false);
            (result, format!("{left} >= {right}: {result}"))
        }

        ComparisonOperator::Lte => {
            let result = compare_numeric(left, right)
                .map(|o| o <= 0)
                .unwrap_or(false);
            (result, format!("{left} <= {right}: {result}"))
        }

        ComparisonOperator::Like => {
            let result = match (left.as_str(), right.as_str()) {
                (Some(l), Some(r)) => like_match(l, r),
                _ => false,
            };
            (result, format!("{left} like {right}: {result}"))
        }

        ComparisonOperator::IsNull => {
            let result = left.is_null();
            (result, format!("{left} is_null: {result}"))
        }

        ComparisonOperator::Exists => {
            // For Relation nodes: ReBAC engine sets a synthetic value.
            // For other nodes: check if value is non-null.
            let result = !left.is_null();
            (result, format!("exists: {result}"))
        }
    }
}

/// Compares two JSON numeric values, returning Ordering as i32 (-1, 0, 1).
fn compare_numeric(left: &JsonValue, right: &JsonValue) -> Option<i32> {
    let l = left.as_f64()?;
    let r = right.as_f64()?;
    if l < r {
        Some(-1)
    } else if l > r {
        Some(1)
    } else {
        Some(0)
    }
}

/// SQL LIKE pattern matching (`%` = any sequence, `_` = any single char).
fn like_match(value: &str, pattern: &str) -> bool {
    // Simple implementation — for production use a dedicated pattern matcher
    let regex_pat = pattern.replace('%', ".*").replace('_', ".");
    regex::Regex::new(&format!("^{}$", regex_pat))
        .map(|re| re.is_match(value))
        .unwrap_or(false)
}

/// Human-readable description of a value source for trace output.
fn describe_source(source: &ValueSource) -> String {
    match source {
        ValueSource::UserAttr { key } => format!("user_attr[{key}]"),
        ValueSource::ResourceField { key } => format!("resource_field[{key}]"),
        ValueSource::Literal { value } => value.to_string(),
        ValueSource::Env { kind } => format!("env[{kind:?}]"),
        ValueSource::Relation { key, target } => format!("relation[{key} → {target}]"),
        ValueSource::ExternalAttr { source, key } => format!("external_attr[{source}.{key}]"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use authz_core::models::policy::{
        ComparisonOperator, ConditionNode, LeafCondition, ValueSource,
    };

    struct NoopJit;

    #[async_trait::async_trait]
    impl JitAttributeFetcher for NoopJit {
        async fn fetch(&self, _: &str, _: &str, _: &str, _: &str) -> Result<JsonValue, AuthzError> {
            Ok(JsonValue::Null)
        }
    }

    fn make_ctx(user_attrs: JsonValue, resource_attrs: JsonValue) -> AuthzContext {
        use authz_core::{
            ids::{TenantId, UserId},
            models::filter::FilterBackend,
        };
        AuthzContext {
            tenant_id: TenantId::new(),
            user_id: UserId::new(),
            user_attributes: user_attrs,
            user_attributes_version: 1,
            resource: crate::context::ResourceContext {
                resource_type: "document".to_owned(),
                resource_ref: None,
                attributes: resource_attrs,
            },
            env: crate::context::EnvContext::default(),
            backend: FilterBackend::Sql,
        }
    }

    #[tokio::test]
    async fn test_eq_user_attr_matches_resource_field() {
        let ctx = make_ctx(
            serde_json::json!({"branch_code": "HN01"}),
            serde_json::json!({"branchCode": "HN01"}),
        );

        let node = ConditionNode::Leaf(LeafCondition {
            left: ValueSource::UserAttr {
                key: "branch_code".to_owned(),
            },
            op: ComparisonOperator::Eq,
            right: ValueSource::ResourceField {
                key: "branchCode".to_owned(),
            },
        });

        let result = evaluate_abac(&node, &ctx, &NoopJit).await.unwrap();
        assert!(result.allowed, "same branch_code should be allowed");
        assert!(result.trace.result);
    }

    #[tokio::test]
    async fn test_eq_mismatch_denied() {
        let ctx = make_ctx(
            serde_json::json!({"branch_code": "HN01"}),
            serde_json::json!({"branchCode": "HCM01"}),
        );

        let node = ConditionNode::Leaf(LeafCondition {
            left: ValueSource::UserAttr {
                key: "branch_code".to_owned(),
            },
            op: ComparisonOperator::Eq,
            right: ValueSource::ResourceField {
                key: "branchCode".to_owned(),
            },
        });

        let result = evaluate_abac(&node, &ctx, &NoopJit).await.unwrap();
        assert!(!result.allowed, "different branch_code should be denied");
        assert!(result.trace.reason.unwrap().contains("!="));
    }

    #[tokio::test]
    async fn test_in_operator_allowed() {
        let ctx = make_ctx(
            serde_json::json!({}),
            serde_json::json!({"status": "PENDING"}),
        );

        let node = ConditionNode::Leaf(LeafCondition {
            left: ValueSource::ResourceField {
                key: "status".to_owned(),
            },
            op: ComparisonOperator::In,
            right: ValueSource::Literal {
                value: serde_json::json!(["PENDING", "DRAFT"]),
            },
        });

        let result = evaluate_abac(&node, &ctx, &NoopJit).await.unwrap();
        assert!(result.allowed);
    }

    #[tokio::test]
    async fn test_and_short_circuits_on_false() {
        let ctx = make_ctx(
            serde_json::json!({"branch_code": "HN01"}),
            serde_json::json!({"branchCode": "HCM01", "status": "ACTIVE"}),
        );

        let node = ConditionNode::And {
            conditions: vec![
                ConditionNode::Leaf(LeafCondition {
                    left: ValueSource::UserAttr {
                        key: "branch_code".to_owned(),
                    },
                    op: ComparisonOperator::Eq,
                    right: ValueSource::ResourceField {
                        key: "branchCode".to_owned(),
                    },
                }),
                ConditionNode::Leaf(LeafCondition {
                    left: ValueSource::ResourceField {
                        key: "status".to_owned(),
                    },
                    op: ComparisonOperator::In,
                    right: ValueSource::Literal {
                        value: serde_json::json!(["PENDING"]),
                    },
                }),
            ],
        };

        let result = evaluate_abac(&node, &ctx, &NoopJit).await.unwrap();
        assert!(!result.allowed);
        // Should have stopped after first false — only 1 child in trace
        assert_eq!(result.trace.children.len(), 1);
    }
}
