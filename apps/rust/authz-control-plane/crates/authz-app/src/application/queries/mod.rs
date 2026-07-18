//! Read-side use-cases. No mutations, no UoW — direct repository reads.

pub mod list_organizations;

pub use list_organizations::{handle as handle_list_organizations, ListOrganizationsQuery};
