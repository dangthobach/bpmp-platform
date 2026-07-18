//! Elasticsearch DSL filter translator (Gap5).
//!
//! Translates `ConditionNode` AST to Elasticsearch Query DSL JSON.
//! Relation nodes are pre-resolved to ID lists and injected as `terms` filters.

use authz_core::{
    models::{
        policy::{ComparisonOperator, ConditionNode, EnvKind, ValueSource},
        resource::ResourceType,
    },
    AuthzError,
};
use serde_json::{json, Value as JsonValue};

use super::translator::{FilterTranslator, TranslatedFilter};
use crate::context::AuthzContext;
use crate::evaluator::rebac::ReBacEngine;

/// Maximum number of terms to inject in a `terms` filter.
/// Elasticsearch hard limit is 65,536.
const MAX_TERMS_SIZE: usize = 1000;

/// Elasticsearch Query DSL filter translator.
///
/// Relation nodes are resolved synchronously before translation by pre-fetching
/// all reachable object IDs from the ReBAC engine (Gap5).
pub struct EsFilterTranslator {
    rebac: std::sync::Arc<ReBacEngine>,
}

impl EsFilterTranslator {
    pub fn new(rebac: std::sync::Arc<ReBacEngine>) -> Self {
        Self { rebac }
    }
}

#[async_trait::async_trait]
impl FilterTranslator for EsFilterTranslator {
    async fn translate(
        &self,
        node: &ConditionNode,
        ctx: &AuthzContext,
        resource_type: &ResourceType,
    ) -> Result<TranslatedFilter, AuthzError> {
        let filter = translate_node(node, ctx, resource_type, &self.rebac).await?;
        Ok(TranslatedFilter::Elasticsearch(filter))
    }
}

async fn translate_node(
    node: &ConditionNode,
    ctx: &AuthzContext,
    rt: &ResourceType,
    rebac: &ReBacEngine,
) -> Result<JsonValue, AuthzError> {
    match node {
        ConditionNode::And { conditions } => {
            let mut must = Vec::with_capacity(conditions.len());
            for c in conditions {
                must.push(Box::pin(translate_node(c, ctx, rt, rebac)).await?);
            }
            Ok(json!({ "bool": { "must": must } }))
        }

        ConditionNode::Or { conditions } => {
            let mut should = Vec::with_capacity(conditions.len());
            for c in conditions {
                should.push(Box::pin(translate_node(c, ctx, rt, rebac)).await?);
            }
            Ok(json!({ "bool": { "should": should, "minimum_should_match": 1 } }))
        }

        ConditionNode::Leaf(leaf) => translate_leaf_es(leaf, ctx, rt, rebac).await,
    }
}

async fn translate_leaf_es(
    leaf: &authz_core::models::policy::LeafCondition,
    ctx: &AuthzContext,
    rt: &ResourceType,
    rebac: &ReBacEngine,
) -> Result<JsonValue, AuthzError> {
    // Handle Relation node specially: pre-fetch IDs
    if let ValueSource::Relation {
        key: relation,
        target,
    } = &leaf.left
    {
        return translate_relation_node(relation, target, ctx, rt, rebac).await;
    }

    let field = match &leaf.left {
        ValueSource::ResourceField { key } => rt.map_field(key, "es"),
        _ => {
            return Err(AuthzError::UnsupportedFilterBackend {
                backend: format!("ES does not support left-side {:?}", leaf.left),
            })
        }
    };

    let right_val = resolve_value_es(&leaf.right, ctx)?;

    let dsl = match leaf.op {
        ComparisonOperator::Eq => json!({ "term": { field: right_val } }),
        ComparisonOperator::Neq => {
            json!({ "bool": { "must_not": [{ "term": { field: right_val } }] } })
        }
        ComparisonOperator::In => json!({ "terms": { field: right_val } }),
        ComparisonOperator::NotIn => {
            json!({ "bool": { "must_not": [{ "terms": { field: right_val } }] } })
        }
        ComparisonOperator::Gte => json!({ "range": { field: { "gte": right_val } } }),
        ComparisonOperator::Lte => json!({ "range": { field: { "lte": right_val } } }),
        ComparisonOperator::Like => {
            let pattern = right_val
                .as_str()
                .unwrap_or("")
                .replace('%', "*")
                .replace('_', "?");
            json!({ "wildcard": { field: { "value": pattern } } })
        }
        ComparisonOperator::IsNull => {
            json!({ "bool": { "must_not": [{ "exists": { "field": field } }] } })
        }
        ComparisonOperator::Exists => {
            json!({ "exists": { "field": field } })
        }
    };

    Ok(dsl)
}

/// Translates a Relation node for Elasticsearch.
///
/// Pre-fetches all reachable object IDs from ReBAC engine and injects as `terms`.
/// If result set is too large, logs a warning and truncates.
async fn translate_relation_node(
    relation: &str,
    _target: &str,
    ctx: &AuthzContext,
    rt: &ResourceType,
    rebac: &ReBacEngine,
) -> Result<JsonValue, AuthzError> {
    let subject = format!("user:{}", ctx.user_id);

    let reachable = rebac
        .resolve_objects(ctx.tenant_id, &subject, relation)
        .await?;

    if reachable.is_empty() {
        // No relations found — DENY all by injecting match_none
        return Ok(json!({ "match_none": {} }));
    }

    let mut ids: Vec<String> = reachable
        .iter()
        .filter_map(|obj| obj.split(':').next_back().map(|id| id.to_owned()))
        .collect();

    if ids.len() > MAX_TERMS_SIZE {
        tracing::warn!(
            count = ids.len(),
            max = MAX_TERMS_SIZE,
            "ReBAC terms filter truncated for ES — consider pre-materializing relations"
        );
        ids.truncate(MAX_TERMS_SIZE);
    }

    // Use the target field (e.g. "resource.owner_id") mapped to ES field name
    let es_field = rt.map_field("id", "es");

    Ok(json!({ "terms": { es_field: ids } }))
}

fn resolve_value_es(source: &ValueSource, ctx: &AuthzContext) -> Result<JsonValue, AuthzError> {
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
