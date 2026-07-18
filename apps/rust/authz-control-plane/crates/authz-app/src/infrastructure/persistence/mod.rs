//! PostgreSQL adapters for the application ports.

pub mod pg_organization_repo;
pub mod pg_outbox_repo;
pub mod pg_unit_of_work;

pub use pg_unit_of_work::{PgUnitOfWork, PgUnitOfWorkFactory};

use sqlx::PgPool;

/// Run the application's own migrations (separate from authz-core's).
pub async fn run_app_migrations(pool: &PgPool) -> Result<(), sqlx::migrate::MigrateError> {
    sqlx::migrate!("./migrations").run(pool).await
}
