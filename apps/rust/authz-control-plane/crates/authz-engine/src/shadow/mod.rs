//! Shadow mode evaluation (G6).
//!
//! Evaluates the SHADOW policy version in parallel with ACTIVE,
//! without blocking the response. Divergences are recorded asynchronously.

pub mod engine;
pub use engine::ShadowEngine;
