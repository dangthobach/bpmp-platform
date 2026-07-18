//! `authz-app` — Application bounded context built on the AuthZ platform.
//!
//! Hexagonal layout:
//!
//! ```text
//! presentation ─► application ─► domain
//!        ▲              │
//!        └── infrastructure (adapters implement ports)
//! ```
//!
//! Plays the role of **PEP** (Policy Enforcement Point): every command and
//! query consults `authz-server` (PDP) before touching domain state.
//!
//! Multi-tenancy is enforced at three layers:
//! 1. HTTP middleware extracts `tenant_id` from a verified JWT.
//! 2. Application use-cases require `Subject` (carries `tenant_id`).
//! 3. SQL repositories always include `WHERE tenant_id = $1` and rely on
//!    Postgres RLS as the last line of defence.

pub mod application;
pub mod bootstrap;
pub mod domain;
pub mod infrastructure;
pub mod presentation;
pub mod telemetry;
