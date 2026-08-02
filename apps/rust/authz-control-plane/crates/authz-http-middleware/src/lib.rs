#![forbid(unsafe_code)]

use std::{num::NonZeroU32, panic::AssertUnwindSafe, str::FromStr, sync::Arc, time::Duration};

use axum::{
    body::Body,
    extract::{Request, State},
    http::{header, Extensions, HeaderName, HeaderValue, StatusCode},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    Json, Router,
};
use futures::FutureExt;
use governor::{DefaultDirectRateLimiter, Quota, RateLimiter};
use serde::Serialize;
use tower_http::limit::RequestBodyLimitLayer;
use tracing::Instrument;
use uuid::Uuid;

pub const REQUEST_ID_HEADER: &str = "x-request-id";
pub const CORRELATION_ID_HEADER: &str = "x-correlation-id";
pub const TRACE_ID_HEADER: &str = "x-trace-id";
pub const TRACE_PARENT_HEADER: &str = "traceparent";

tokio::task_local! {
    static ACTIVE_REQUEST_METADATA: RequestMetadata;
}

#[derive(Debug, Clone)]
pub struct Config {
    pub service: String,
    pub request_timeout: Duration,
    pub max_body_bytes: usize,
    pub rate_limit_requests_per_second: u32,
    pub rate_limit_burst: u32,
}

impl Config {
    pub fn validate(&self) -> Result<(), &'static str> {
        if self.service.trim().is_empty() {
            return Err("HTTP middleware service name is required");
        }
        if self.request_timeout.is_zero()
            || self.max_body_bytes == 0
            || self.rate_limit_requests_per_second == 0
            || self.rate_limit_burst == 0
        {
            return Err("HTTP middleware bounds must be positive");
        }
        Ok(())
    }
}

#[derive(Clone)]
struct TransportState {
    config: Config,
    limiter: Arc<DefaultDirectRateLimiter>,
}

#[derive(Debug, Clone)]
pub struct RequestId(pub String);

#[derive(Debug, Clone)]
pub struct RequestMetadata {
    pub request_id: String,
    pub correlation_id: String,
    pub trace_id: String,
}

pub fn metadata_from_extensions(extensions: &Extensions) -> RequestMetadata {
    extensions
        .get::<RequestMetadata>()
        .cloned()
        .unwrap_or_else(|| {
            let request_id = Uuid::new_v4().simple().to_string();
            RequestMetadata {
                correlation_id: request_id.clone(),
                trace_id: Uuid::new_v4().simple().to_string(),
                request_id,
            }
        })
}

#[derive(Debug, Serialize)]
pub struct Problem {
    pub r#type: String,
    pub title: String,
    pub status: u16,
    pub code: String,
    pub request_id: String,
    pub correlation_id: String,
    pub trace_id: String,
    pub retryable: bool,
}

pub fn apply(router: Router, config: Config) -> Result<Router, &'static str> {
    config.validate()?;
    let max_body_bytes = config.max_body_bytes;
    let quota = Quota::per_second(
        NonZeroU32::new(config.rate_limit_requests_per_second)
            .ok_or("HTTP rate limit must be positive")?,
    )
    .allow_burst(
        NonZeroU32::new(config.rate_limit_burst).ok_or("HTTP rate limit burst must be positive")?,
    );
    let state = TransportState {
        config,
        limiter: Arc::new(RateLimiter::direct(quota)),
    };
    Ok(router
        .layer(RequestBodyLimitLayer::new(max_body_bytes))
        .layer(middleware::from_fn_with_state(state, transport)))
}

async fn transport(
    State(state): State<TransportState>,
    mut request: Request,
    next: Next,
) -> Response {
    let config = &state.config;
    let started = std::time::Instant::now();
    let request_id = normalized_header(&request, REQUEST_ID_HEADER)
        .unwrap_or_else(|| Uuid::new_v4().simple().to_string());
    let correlation_id =
        normalized_header(&request, CORRELATION_ID_HEADER).unwrap_or_else(|| request_id.clone());
    let trace_id = normalized_header(&request, TRACE_PARENT_HEADER)
        .and_then(|value| trace_id_from_parent(&value))
        .unwrap_or_else(|| Uuid::new_v4().simple().to_string());
    let metadata = RequestMetadata {
        request_id: request_id.clone(),
        correlation_id: correlation_id.clone(),
        trace_id: trace_id.clone(),
    };
    request
        .extensions_mut()
        .insert(RequestId(request_id.clone()));
    request.extensions_mut().insert(metadata.clone());
    let method = request.method().clone();
    let path = request.uri().path().to_owned();
    let span = tracing::info_span!(
        "http.request",
        service = %config.service,
        request_id = %request_id,
        correlation_id = %correlation_id,
        trace_id = %trace_id,
        http.method = %method,
    );

    let mut response = if state.limiter.check().is_err() {
        problem_response(
            StatusCode::TOO_MANY_REQUESTS,
            "rate_limit_exceeded",
            "Rate limit exceeded",
            &metadata,
            true,
        )
    } else {
        let execution = ACTIVE_REQUEST_METADATA.scope(metadata.clone(), next.run(request));
        let execution = AssertUnwindSafe(execution).catch_unwind();
        match tokio::time::timeout(config.request_timeout, execution)
            .instrument(span)
            .await
        {
            Ok(Ok(response)) => response,
            Ok(Err(_)) => {
                tracing::error!(
                    service = %config.service,
                    request_id = %request_id,
                    correlation_id = %correlation_id,
                    trace_id = %trace_id,
                    "HTTP handler panic recovered"
                );
                problem_response(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "internal_error",
                    "Internal server error",
                    &metadata,
                    false,
                )
            }
            Err(_) => problem_response(
                StatusCode::GATEWAY_TIMEOUT,
                "request_timeout",
                "Request timed out",
                &metadata,
                true,
            ),
        }
    };

    if response.status() == StatusCode::PAYLOAD_TOO_LARGE {
        response = problem_response(
            StatusCode::PAYLOAD_TOO_LARGE,
            "request_too_large",
            "Request body is too large",
            &metadata,
            false,
        );
    }
    set_response_headers(&mut response, &metadata);
    let status = response.status();
    tracing::info!(
        service = %config.service,
        request_id = %request_id,
        correlation_id = %correlation_id,
        trace_id = %trace_id,
        http.method = %method,
        http.path = %path,
        http.status_code = status.as_u16(),
        duration_ms = started.elapsed().as_millis() as u64,
        "HTTP request completed"
    );
    response
}

pub fn problem_response(
    status: StatusCode,
    code: &str,
    title: &str,
    metadata: &RequestMetadata,
    retryable: bool,
) -> Response {
    let metadata = ACTIVE_REQUEST_METADATA
        .try_with(Clone::clone)
        .unwrap_or_else(|_| metadata.clone());
    let body = Problem {
        r#type: format!("https://docs.bpmp.dev/problems/{code}"),
        title: title.to_owned(),
        status: status.as_u16(),
        code: code.to_owned(),
        request_id: metadata.request_id,
        correlation_id: metadata.correlation_id,
        trace_id: metadata.trace_id,
        retryable,
    };
    let mut response = (status, Json(body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/problem+json"),
    );
    response
}

fn normalized_header(request: &Request<Body>, name: &str) -> Option<String> {
    request
        .headers()
        .get(name)
        .and_then(|value| value.to_str().ok())
        .and_then(normalized_id)
}

fn normalized_id(value: &str) -> Option<String> {
    let value = value.trim();
    if value.is_empty() || value.len() > 128 {
        return None;
    }
    if value
        .bytes()
        .all(|value| value.is_ascii_alphanumeric() || matches!(value, b'-' | b'_' | b'.' | b':'))
    {
        Some(value.to_owned())
    } else {
        None
    }
}

fn trace_id_from_parent(value: &str) -> Option<String> {
    let mut parts = value.split('-');
    let version = parts.next()?;
    let trace_id = parts.next()?;
    let parent_id = parts.next()?;
    let flags = parts.next()?;
    if parts.next().is_some()
        || version != "00"
        || trace_id.len() != 32
        || parent_id.len() != 16
        || flags.len() != 2
        || trace_id.bytes().all(|value| value == b'0')
        || parent_id.bytes().all(|value| value == b'0')
        || !trace_id.bytes().all(is_lower_hex)
        || !parent_id.bytes().all(is_lower_hex)
        || !flags.bytes().all(is_lower_hex)
    {
        return None;
    }
    Some(trace_id.to_owned())
}

const fn is_lower_hex(value: u8) -> bool {
    value.is_ascii_digit() || (value >= b'a' && value <= b'f')
}

fn set_response_headers(response: &mut Response, metadata: &RequestMetadata) {
    for (name, value) in [
        (REQUEST_ID_HEADER, metadata.request_id.as_str()),
        (CORRELATION_ID_HEADER, metadata.correlation_id.as_str()),
        (TRACE_ID_HEADER, metadata.trace_id.as_str()),
    ] {
        if let (Ok(name), Ok(value)) = (HeaderName::from_str(name), HeaderValue::from_str(value)) {
            response.headers_mut().insert(name, value);
        }
    }
    response.headers_mut().insert(
        HeaderName::from_static("x-content-type-options"),
        HeaderValue::from_static("nosniff"),
    );
    response.headers_mut().insert(
        HeaderName::from_static("x-frame-options"),
        HeaderValue::from_static("DENY"),
    );
    response.headers_mut().insert(
        HeaderName::from_static("referrer-policy"),
        HeaderValue::from_static("no-referrer"),
    );
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::to_bytes, routing::get, Router};
    use tower::ServiceExt;

    async fn panic_handler() -> StatusCode {
        panic!("secret")
    }

    #[tokio::test]
    async fn panic_is_mapped_to_canonical_problem() {
        let router = apply(
            Router::new().route("/panic", get(panic_handler)),
            Config {
                service: "test".to_owned(),
                request_timeout: Duration::from_secs(1),
                max_body_bytes: 1024,
                rate_limit_requests_per_second: 100,
                rate_limit_burst: 100,
            },
        )
        .expect("valid middleware config");
        let response = router
            .oneshot(
                Request::builder()
                    .uri("/panic")
                    .header(REQUEST_ID_HEADER, "request-1")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
        assert_eq!(
            response.headers().get(REQUEST_ID_HEADER),
            Some(&HeaderValue::from_static("request-1"))
        );
    }

    #[tokio::test]
    async fn inner_problem_uses_transport_owned_metadata() {
        let router = apply(
            Router::new().route(
                "/problem",
                get(|| async {
                    problem_response(
                        StatusCode::BAD_REQUEST,
                        "invalid_request",
                        "Invalid request",
                        &RequestMetadata {
                            request_id: "handler-request".to_owned(),
                            correlation_id: "handler-correlation".to_owned(),
                            trace_id: "handler-trace".to_owned(),
                        },
                        false,
                    )
                }),
            ),
            Config {
                service: "test".to_owned(),
                request_timeout: Duration::from_secs(1),
                max_body_bytes: 1024,
                rate_limit_requests_per_second: 100,
                rate_limit_burst: 100,
            },
        )
        .expect("valid middleware config");
        let response = router
            .oneshot(
                Request::builder()
                    .uri("/problem")
                    .header(REQUEST_ID_HEADER, "transport-request")
                    .header(CORRELATION_ID_HEADER, "transport-correlation")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        let body = to_bytes(response.into_body(), 1024)
            .await
            .expect("problem body");
        let problem: serde_json::Value = serde_json::from_slice(&body).expect("problem JSON");
        assert_eq!(problem["request_id"], "transport-request");
        assert_eq!(problem["correlation_id"], "transport-correlation");
    }

    #[tokio::test]
    async fn timeout_is_bounded_and_retryable() {
        let router = apply(
            Router::new().route(
                "/slow",
                get(|| async {
                    tokio::time::sleep(Duration::from_secs(1)).await;
                    StatusCode::NO_CONTENT
                }),
            ),
            Config {
                service: "test".to_owned(),
                request_timeout: Duration::from_millis(5),
                max_body_bytes: 1024,
                rate_limit_requests_per_second: 100,
                rate_limit_burst: 100,
            },
        )
        .expect("valid middleware config");
        let response = router
            .oneshot(
                Request::builder()
                    .uri("/slow")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::GATEWAY_TIMEOUT);
    }

    #[tokio::test]
    async fn admission_rate_limit_is_fail_closed() {
        let router = apply(
            Router::new().route("/limited", get(|| async { StatusCode::NO_CONTENT })),
            Config {
                service: "test".to_owned(),
                request_timeout: Duration::from_secs(1),
                max_body_bytes: 1024,
                rate_limit_requests_per_second: 1,
                rate_limit_burst: 1,
            },
        )
        .expect("valid middleware config");
        let request = || {
            Request::builder()
                .uri("/limited")
                .body(Body::empty())
                .expect("request")
        };
        assert_eq!(
            router
                .clone()
                .oneshot(request())
                .await
                .expect("response")
                .status(),
            StatusCode::NO_CONTENT
        );
        assert_eq!(
            router.oneshot(request()).await.expect("response").status(),
            StatusCode::TOO_MANY_REQUESTS
        );
    }
}
