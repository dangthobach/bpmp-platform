//! FilterTranslator trait and registry.
//!
//! ## Design (G5, EC-2 Gap5)
//! A single policy AST can be translated to SQL, Elasticsearch DSL, or MongoDB
//! `$match` by swapping the `FilterTranslator` implementation.
//!
//! The `FilterTranslatorRegistry` holds all backend translators and dispatches
//! based on the requested backend at evaluation time.

use authz_core::{
    models::{
        filter::{FilterBackend, SqlFilterResult},
        policy::ConditionNode,
        resource::ResourceType,
    },
    AuthzError,
};
use serde_json::Value as JsonValue;

use crate::context::AuthzContext;

/// The output of translating an AST for a specific backend.
#[derive(Debug, Clone)]
pub enum TranslatedFilter {
    Sql(SqlFilterResult),
    Elasticsearch(JsonValue),
    Mongodb(JsonValue),
}

/// Trait for translating a backend-agnostic condition AST to a backend-specific filter.
///
/// Implementations: `SqlFilterTranslator`, `EsFilterTranslator`, `MongoFilterTranslator`.
#[async_trait::async_trait]
pub trait FilterTranslator: Send + Sync {
    /// Translates the AST for this backend.
    async fn translate(
        &self,
        node: &ConditionNode,
        ctx: &AuthzContext,
        resource_type: &ResourceType,
    ) -> Result<TranslatedFilter, AuthzError>;
}

/// Registry holding one translator per backend.
///
/// Use `dispatch` to route an AST to the correct translator.
pub struct FilterTranslatorRegistry {
    sql: Box<dyn FilterTranslator>,
    elasticsearch: Box<dyn FilterTranslator>,
    mongodb: Box<dyn FilterTranslator>,
}

impl FilterTranslatorRegistry {
    pub fn new(
        sql: Box<dyn FilterTranslator>,
        elasticsearch: Box<dyn FilterTranslator>,
        mongodb: Box<dyn FilterTranslator>,
    ) -> Self {
        Self {
            sql,
            elasticsearch,
            mongodb,
        }
    }

    /// Routes the translation to the correct backend translator.
    pub async fn dispatch(
        &self,
        backend: FilterBackend,
        node: &ConditionNode,
        ctx: &AuthzContext,
        resource_type: &ResourceType,
    ) -> Result<TranslatedFilter, AuthzError> {
        match backend {
            FilterBackend::Sql => self.sql.translate(node, ctx, resource_type).await,
            FilterBackend::Elasticsearch => {
                self.elasticsearch.translate(node, ctx, resource_type).await
            }
            FilterBackend::Mongodb => self.mongodb.translate(node, ctx, resource_type).await,
        }
    }
}
