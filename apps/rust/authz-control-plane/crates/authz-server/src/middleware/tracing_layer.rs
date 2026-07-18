//! Tracing layer configuration for HTTP request logging.
//!
//! Logs each request with method, path, status code, and latency.

pub use tower_http::trace::TraceLayer;
