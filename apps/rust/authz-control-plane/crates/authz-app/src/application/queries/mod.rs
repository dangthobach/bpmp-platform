//! Read-side use-cases. No mutations, no UoW — direct repository reads.

pub mod get_organization;
pub mod list_organizations;
pub mod resolve_branch_scope;

pub use get_organization::{handle as handle_get_organization, GetOrganizationQuery};
pub use list_organizations::{handle as handle_list_organizations, ListOrganizationsQuery};
pub use resolve_branch_scope::{handle as handle_resolve_branch_scope, ResolveBranchScopeQuery};
