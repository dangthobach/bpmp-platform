//! Write-side use-cases.
//!
//! Template:
//! 1. PEP check via [`AuthzPort`].
//! 2. Begin UoW.
//! 3. Load → mutate aggregate (domain rules).
//! 4. Save with optimistic-lock version.
//! 5. Enqueue domain events into outbox (same tx).
//! 6. Commit.

pub mod add_node;
pub mod create_organization;
pub mod move_node;

pub use add_node::{handle as handle_add_node, AddNodeCommand};
pub use create_organization::{handle as handle_create_organization, CreateOrganizationCommand};
pub use move_node::{handle as handle_move_node, MoveNodeCommand};
