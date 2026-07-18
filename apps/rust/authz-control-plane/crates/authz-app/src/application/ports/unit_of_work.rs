//! Unit of Work — the single atomic boundary for write use-cases.
//!
//! Holds a DB transaction; exposes the repositories scoped to that tx.
//! `commit` and `rollback` are mutually exclusive; dropping without
//! commit MUST roll back.

use async_trait::async_trait;

use crate::application::errors::AppError;
use crate::application::ports::organization_repo::OrganizationRepository;
use crate::application::ports::outbox::OutboxRepository;

#[async_trait]
pub trait UnitOfWork: Send {
    fn organizations(&mut self) -> &mut dyn OrganizationRepository;
    fn outbox(&mut self) -> &mut dyn OutboxRepository;
    async fn commit(self: Box<Self>) -> Result<(), AppError>;
}

#[async_trait]
pub trait UnitOfWorkFactory: Send + Sync {
    async fn begin(&self) -> Result<Box<dyn UnitOfWork>, AppError>;
}
