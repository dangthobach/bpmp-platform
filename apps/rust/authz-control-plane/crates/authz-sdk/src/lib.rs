//! `authz-sdk` — Client SDK for the AuthZ Decision Plane (PDP).
//!
//! Used by PEP (Policy Enforcement Point) applications such as `authz-app`
//! to call `/authz/v1/check`, `/authz/v1/filter`, `/authz/v1/explain` with:
//!
//! * In-process **decision cache** keyed by `(tenant, user, action, attrs_version)`
//!   — cache invalidates automatically when the user's `attributes_version` bumps.
//! * **Batch executor** to fold N independent checks into 1 call (anti-N+1).
//! * Uniform [`SdkError`] mapped to safe HTTP semantics.
//!
//! ## Layering
//! - [`client`]      — transport (HTTP today, gRPC tomorrow).
//! - [`cache`]       — versioned decision cache decorator.
//! - [`batch`]       — request fanout / deduplication.
//! - [`types`]       — wire-level DTOs shared with `authz-server`.
//! - [`envelope`]    — uniform `EnvelopeResponse<T>` for PEPs to reuse.

pub mod batch;
pub mod cache;
pub mod client;
pub mod envelope;
pub mod error;
pub mod types;

pub use cache::CachedAuthzClient;
pub use client::{AuthzClient, HttpAuthzClient, HttpAuthzClientConfig};
pub use envelope::EnvelopeResponse;
pub use error::SdkError;
pub use types::{
    CheckRequest, CheckResponse, Decision, ExplainRequest, ExplainResponse, FilterRequest,
    FilterResponse, Subject,
};
