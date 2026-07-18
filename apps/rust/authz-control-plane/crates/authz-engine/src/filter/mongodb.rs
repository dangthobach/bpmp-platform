//! MongoDB `$match` filter translator (Gap5).

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

/// MongoDB `$match` expression translator.
pub struct MongoFilterTranslator {
    rebac: std::sync::Arc<ReBacEngine>,
}

impl MongoFilterTranslator {
    pub fn new(rebac: std::sync::Arc<ReBacEngine>) -> Self {
        Self { rebac }
    }
}

#[async_trait::async_trait]
impl FilterTranslator for MongoFilterTranslator {
    async fn translate(
        &self,
        node: &ConditionNode,
        ctx: &AuthzContext,
        resource_type: &ResourceType,
    ) -> Result<TranslatedFilter, AuthzError> {
        let filter = translate_node(node, ctx, resource_type, &self.rebac).await?;
        Ok(TranslatedFilter::Mongodb(filter))
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
            let mut clauses = Vec::with_capacity(conditions.len());
            for c in conditions {
                clauses.push(Box::pin(translate_node(c, ctx, rt, rebac)).await?);
            }
            Ok(json!({ "$and": clauses }))
        }

        ConditionNode::Or { conditions } => {
            let mut clauses = Vec::with_capacity(conditions.len());
            for c in conditions {
                clauses.push(Box::pin(translate_node(c, ctx, rt, rebac)).await?);
            }
            Ok(json!({ "$or": clauses }))
        }

        ConditionNode::Leaf(leaf) => translate_leaf_mongo(leaf, ctx, rt, rebac).await,
    }
}

async fn translate_leaf_mongo(
    leaf: &authz_core::models::policy::LeafCondition,
    ctx: &AuthzContext,
    rt: &ResourceType,
    rebac: &ReBacEngine,
) -> Result<JsonValue, AuthzError> {
    if let ValueSource::Relation { key: relation, .. } = &leaf.left {
        let subject = format!("user:{}", ctx.user_id);
        let reachable = rebac
            .resolve_objects(ctx.tenant_id, &subject, relation)
            .await?;

        if reachable.is_empty() {
            // match nothing
            return Ok(json!({ "_id": { "$exists": false } }));
        }

        let ids: Vec<&str> = reachable
            .iter()
            .filter_map(|obj| obj.split(':').next_back())
            .collect();

        let field = rt.map_field("id", "mongo");
        return Ok(json!({ field: { "$in": ids } }));
    }

    let field = match &leaf.left {
        ValueSource::ResourceField { key } => rt.map_field(key, "mongo"),
        _ => {
            return Err(AuthzError::UnsupportedFilterBackend {
                backend: format!("MongoDB does not support left-side {:?}", leaf.left),
            })
        }
    };

    let right_val = resolve_value_mongo(&leaf.right, ctx)?;

    let filter = match leaf.op {
        ComparisonOperator::Eq => json!({ field: { "$eq": right_val } }),
        ComparisonOperator::Neq => json!({ field: { "$ne": right_val } }),
        ComparisonOperator::In => json!({ field: { "$in": right_val } }),
        ComparisonOperator::NotIn => json!({ field: { "$nin": right_val } }),
        ComparisonOperator::Gte => json!({ field: { "$gte": right_val } }),
        ComparisonOperator::Lte => json!({ field: { "$lte": right_val } }),
        ComparisonOperator::Like => {
            let pattern = right_val
                .as_str()
                .unwrap_or("")
                .replace('%', ".*")
                .replace('_', ".");
            json!({ field: { "$regex": pattern, "$options": "i" } })
        }
        ComparisonOperator::IsNull => json!({ field: { "$exists": false } }),
        ComparisonOperator::Exists => json!({ field: { "$exists": true } }),
    };

    Ok(filter)
}

fn resolve_value_mongo(source: &ValueSource, ctx: &AuthzContext) -> Result<JsonValue, AuthzError> {
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
