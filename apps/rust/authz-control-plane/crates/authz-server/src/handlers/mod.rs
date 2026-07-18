//! HTTP handlers module.

pub mod admin;
pub mod check;
pub mod explain;
pub mod filter;
pub mod health;
pub mod relations;

pub use check::check_handler;
pub use explain::explain_handler;
pub use filter::filter_handler;
pub use health::health_handler;
pub use relations::insert_relation_handler;
