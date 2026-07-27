//! Read-side use-cases. No mutations, no UoW — direct repository reads.

pub mod get_organization;
pub mod list_organizations;

pub use get_organization::{handle as handle_get_organization, GetOrganizationQuery};
pub use list_organizations::{handle as handle_list_organizations, ListOrganizationsQuery};
