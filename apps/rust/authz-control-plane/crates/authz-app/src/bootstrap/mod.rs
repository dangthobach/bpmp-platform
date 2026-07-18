//! Composition root: wires concrete adapters into the trait-based
//! application layer.
//!
//! The container is the ONLY place that knows about both `authz-sdk`
//! transports and `sqlx` pools. Use-cases continue to depend on traits.

use std::sync::Arc;
use std::time::Duration;

use sqlx::PgPool;

use authz_sdk::{AuthzClient, CachedAuthzClient, HttpAuthzClient, HttpAuthzClientConfig};

use crate::application::commands;
use crate::application::ports::authz_port::AuthzPort;
use crate::application::ports::unit_of_work::UnitOfWorkFactory;
use crate::application::queries;
use crate::infrastructure::authz_adapter::SdkAuthzAdapter;
use crate::infrastructure::config::AppConfig;
use crate::infrastructure::persistence::PgUnitOfWorkFactory;

/// Wide, cheap-to-clone container injected into Axum state.
#[derive(Clone)]
pub struct AppContainer {
    pub pool: PgPool,
    pub authz_client: Arc<dyn AuthzClient>,
    pub authz: Arc<dyn AuthzPort>,
    pub uow_factory: Arc<dyn UnitOfWorkFactory>,
}

impl AppContainer {
    pub fn build(cfg: &AppConfig, pool: PgPool) -> anyhow::Result<Self> {
        let http = HttpAuthzClient::new(HttpAuthzClientConfig {
            base_url: cfg.authz_pdp_url.clone(),
            timeout: cfg.authz_timeout(),
            connect_timeout: Duration::from_millis(200),
            auth_token: None,
        })?;
        let cached = CachedAuthzClient::new(http, cfg.authz_cache_capacity, cfg.authz_cache_ttl());
        let authz_client: Arc<dyn AuthzClient> = Arc::new(cached);
        let authz: Arc<dyn AuthzPort> = Arc::new(SdkAuthzAdapter::new(authz_client.clone(), false));
        let uow_factory: Arc<dyn UnitOfWorkFactory> =
            Arc::new(PgUnitOfWorkFactory::new(pool.clone()));
        Ok(Self {
            pool,
            authz_client,
            authz,
            uow_factory,
        })
    }

    pub fn create_org_deps(&self) -> commands::create_organization::Deps {
        commands::create_organization::Deps {
            authz: self.authz.clone(),
            uow_factory: self.uow_factory.clone(),
        }
    }
    pub fn add_node_deps(&self) -> commands::add_node::Deps {
        commands::add_node::Deps {
            authz: self.authz.clone(),
            uow_factory: self.uow_factory.clone(),
        }
    }
    pub fn move_node_deps(&self) -> commands::move_node::Deps {
        commands::move_node::Deps {
            authz: self.authz.clone(),
            uow_factory: self.uow_factory.clone(),
        }
    }
    pub fn list_orgs_deps(&self) -> queries::list_organizations::Deps {
        queries::list_organizations::Deps {
            authz: self.authz.clone(),
            uow_factory: self.uow_factory.clone(),
        }
    }
}
