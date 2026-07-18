//! Outbound ports (hexagonal).
//!
//! Application code talks to these traits; infrastructure provides
//! implementations. This direction of dependency lets us swap Postgres
//! for any store and the authz HTTP client for any other transport
//! without touching use-cases.

pub mod authz_port;
pub mod organization_repo;
pub mod outbox;
pub mod unit_of_work;

pub use authz_port::{AuthzPort, ResourceRef, SqlFilter, Subject};
pub use organization_repo::OrganizationRepository;
pub use outbox::OutboxRepository;
pub use unit_of_work::{UnitOfWork, UnitOfWorkFactory};
