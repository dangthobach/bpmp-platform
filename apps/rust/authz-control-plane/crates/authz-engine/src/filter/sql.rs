//! SQL filter translator.
//!
//! Translates a backend-agnostic `ConditionNode` AST into a parameterized
//! SQL WHERE clause. Uses `$1`, `$2`… placeholders for sqlx.

use authz_core::{
    models::{
        filter::SqlFilterResult,
        policy::{ComparisonOperator, ConditionNode, EnvKind, ValueSource},
        resource::ResourceType,
    },
    AuthzError,
};
use serde_json::Value as JsonValue;
use std::collections::HashMap;

use super::translator::{FilterTranslator, TranslatedFilter};
use crate::context::AuthzContext;

/// SQL WHERE clause translator.
///
/// Produces a parameterized SQL predicate string and a map of parameter values.
/// Callers inject the predicate into their query template.
pub struct SqlFilterTranslator;

#[async_trait::async_trait]
impl FilterTranslator for SqlFilterTranslator {
    async fn translate(
        &self,
        node: &ConditionNode,
        ctx: &AuthzContext,
        resource_type: &ResourceType,
    ) -> Result<TranslatedFilter, AuthzError> {
        let mut params: Vec<JsonValue> = Vec::new();
        let predicate = translate_node(node, ctx, resource_type, &mut params)?;

        // Build named parameter map: p1, p2, ...
        let param_map: HashMap<String, JsonValue> = params
            .into_iter()
            .enumerate()
            .map(|(i, v)| (format!("p{}", i + 1), v))
            .collect();

        Ok(TranslatedFilter::Sql(SqlFilterResult {
            predicate,
            params: param_map,
        }))
    }
}

fn translate_node(
    node: &ConditionNode,
    ctx: &AuthzContext,
    rt: &ResourceType,
    params: &mut Vec<JsonValue>,
) -> Result<String, AuthzError> {
    match node {
        ConditionNode::And { conditions } => {
            let parts: Result<Vec<_>, _> = conditions
                .iter()
                .map(|c| translate_node(c, ctx, rt, params))
                .collect();
            Ok(format!("({})", parts?.join(" AND ")))
        }

        ConditionNode::Or { conditions } => {
            let parts: Result<Vec<_>, _> = conditions
                .iter()
                .map(|c| translate_node(c, ctx, rt, params))
                .collect();
            Ok(format!("({})", parts?.join(" OR ")))
        }

        ConditionNode::Leaf(leaf) => {
            translate_leaf(&leaf.left, &leaf.op, &leaf.right, ctx, rt, params)
        }
    }
}

fn translate_leaf(
    left: &ValueSource,
    op: &ComparisonOperator,
    right: &ValueSource,
    ctx: &AuthzContext,
    rt: &ResourceType,
    params: &mut Vec<JsonValue>,
) -> Result<String, AuthzError> {
    match left {
        ValueSource::ResourceField { key } => {
            let col = rt.map_field(key, "sql");
            let right_val = resolve_right_for_sql(right, ctx)?;

            let placeholder = format!("${}", params.len() + 1);

            let sql = match op {
                ComparisonOperator::Eq => {
                    params.push(right_val);
                    format!("{col} = {placeholder}")
                }
                ComparisonOperator::Neq => {
                    params.push(right_val);
                    format!("{col} != {placeholder}")
                }
                ComparisonOperator::In => {
                    params.push(right_val);
                    format!("{col} = ANY({placeholder})")
                }
                ComparisonOperator::NotIn => {
                    params.push(right_val);
                    format!("{col} != ALL({placeholder})")
                }
                ComparisonOperator::Gte => {
                    params.push(right_val);
                    format!("{col} >= {placeholder}")
                }
                ComparisonOperator::Lte => {
                    params.push(right_val);
                    format!("{col} <= {placeholder}")
                }
                ComparisonOperator::Like => {
                    params.push(right_val);
                    format!("{col} LIKE {placeholder}")
                }
                ComparisonOperator::IsNull => {
                    format!("{col} IS NULL")
                }
                ComparisonOperator::Exists => {
                    // Relation nodes: inject pre-fetched IDs via params
                    params.push(right_val);
                    format!("{col} = ANY({placeholder})")
                }
            };

            Ok(sql)
        }

        // User attribute on the left is resolved to a literal and compared to resource field
        ValueSource::UserAttr { key } => {
            let val = ctx.user_attr(key).cloned().unwrap_or(JsonValue::Null);
            let placeholder = format!("${}", params.len() + 1);
            params.push(val);

            let right_col = match right {
                ValueSource::ResourceField { key } => rt.map_field(key, "sql"),
                _ => {
                    return Err(AuthzError::UnsupportedOperator {
                        operator: format!("{op:?}"),
                        node_type: "UserAttr-left with non-ResourceField right".to_owned(),
                    })
                }
            };

            let sql = match op {
                ComparisonOperator::Eq => format!("{placeholder} = {right_col}"),
                ComparisonOperator::Neq => format!("{placeholder} != {right_col}"),
                _ => {
                    return Err(AuthzError::UnsupportedOperator {
                        operator: format!("{op:?}"),
                        node_type: "UserAttr comparison".to_owned(),
                    })
                }
            };

            Ok(sql)
        }

        _ => Err(AuthzError::UnsupportedOperator {
            operator: format!("{op:?}"),
            node_type: format!("left={left:?}"),
        }),
    }
}

fn resolve_right_for_sql(
    source: &ValueSource,
    ctx: &AuthzContext,
) -> Result<JsonValue, AuthzError> {
    match source {
        ValueSource::Literal { value } => Ok(value.clone()),
        ValueSource::UserAttr { key } => Ok(ctx.user_attr(key).cloned().unwrap_or(JsonValue::Null)),
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
        _ => Ok(JsonValue::Null),
    }
}
