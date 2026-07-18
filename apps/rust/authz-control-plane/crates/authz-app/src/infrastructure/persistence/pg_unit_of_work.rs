//! Unit-of-Work backed by a single `sqlx` transaction.
//!
//! The struct itself implements both repository traits — callers obtain a
//! `&mut dyn` through the [`UnitOfWork`] facade. This keeps the borrow checker
//! happy: each `uow.organizations().<method>` borrow is statement-scoped, and
//! `commit` consumes the UoW so no further repo calls are possible.

use async_trait::async_trait;
use sqlx::{PgPool, Postgres, Transaction};
use uuid::Uuid;

use crate::application::errors::AppError;
use crate::application::ports::organization_repo::{OrganizationListItem, OrganizationRepository};
use crate::application::ports::outbox::{OutboxRecord, OutboxRepository};
use crate::application::ports::unit_of_work::{UnitOfWork, UnitOfWorkFactory};
use crate::domain::organization::{OrgId, Organization};

use super::{pg_organization_repo, pg_outbox_repo};

pub struct PgUnitOfWorkFactory {
    pool: PgPool,
}

impl PgUnitOfWorkFactory {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[async_trait]
impl UnitOfWorkFactory for PgUnitOfWorkFactory {
    async fn begin(&self) -> Result<Box<dyn UnitOfWork>, AppError> {
        let tx = self.pool.begin().await?;
        Ok(Box::new(PgUnitOfWork { tx: Some(tx) }))
    }
}

pub struct PgUnitOfWork {
    tx: Option<Transaction<'static, Postgres>>,
}

impl PgUnitOfWork {
    fn tx(&mut self) -> Result<&mut Transaction<'static, Postgres>, AppError> {
        self.tx
            .as_mut()
            .ok_or_else(|| AppError::Internal("uow already committed".into()))
    }
}

#[async_trait]
impl UnitOfWork for PgUnitOfWork {
    fn organizations(&mut self) -> &mut dyn OrganizationRepository {
        self
    }
    fn outbox(&mut self) -> &mut dyn OutboxRepository {
        self
    }

    async fn commit(mut self: Box<Self>) -> Result<(), AppError> {
        if let Some(tx) = self.tx.take() {
            tx.commit().await?;
        }
        Ok(())
    }
}

#[async_trait]
impl OrganizationRepository for PgUnitOfWork {
    async fn load(&mut self, tenant: Uuid, id: OrgId) -> Result<Organization, AppError> {
        pg_organization_repo::load_aggregate(self.tx()?, tenant, id).await
    }
    async fn save(&mut self, org: &Organization, expected: i64) -> Result<(), AppError> {
        pg_organization_repo::save_aggregate(self.tx()?, org, expected).await
    }
    async fn list(
        &mut self,
        tenant: Uuid,
        offset: i64,
        limit: i64,
    ) -> Result<Vec<OrganizationListItem>, AppError> {
        pg_organization_repo::list_organizations(self.tx()?, tenant, offset, limit).await
    }
}

#[async_trait]
impl OutboxRepository for PgUnitOfWork {
    async fn enqueue(&mut self, record: OutboxRecord) -> Result<(), AppError> {
        pg_outbox_repo::enqueue(self.tx()?, record).await
    }
}
