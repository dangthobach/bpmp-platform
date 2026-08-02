#![forbid(unsafe_code)]

use futures::{FutureExt, future::BoxFuture};
use http::{HeaderMap, HeaderValue, Request, Response};
use sha2::{Digest, Sha256};
use std::collections::BTreeSet;
use std::panic::AssertUnwindSafe;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use std::time::{Duration, Instant};
use tonic::transport::server::{TcpConnectInfo, TlsConnectInfo};
use tower::{Layer, Service};
use tracing::{Instrument, info, info_span, warn};
use uuid::Uuid;

pub const REQUEST_ID_HEADER: &str = "x-bpmp-request-id";
pub const CORRELATION_ID_HEADER: &str = "x-bpmp-correlation-id";
pub const TENANT_ID_HEADER: &str = "x-bpmp-tenant-id";
pub const COMMAND_ID_HEADER: &str = "x-bpmp-command-id";
pub const TRACE_ID_HEADER: &str = "x-bpmp-trace-id";
pub const TRACE_PARENT_HEADER: &str = "traceparent";
pub const TRACE_STATE_HEADER: &str = "tracestate";

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RequestMetadata {
    pub request_id: String,
    pub correlation_id: String,
    pub tenant_id: String,
    pub command_id: String,
    pub trace_id: String,
    pub trace_parent: String,
    pub trace_state: String,
}

impl RequestMetadata {
    #[must_use]
    pub fn from_headers(headers: &HeaderMap) -> Self {
        let incoming_trace_parent = header(headers, TRACE_PARENT_HEADER)
            .filter(|value| trace_id_from_parent(value).is_some());
        let trace_id = incoming_trace_parent
            .and_then(trace_id_from_parent)
            .unwrap_or_else(generated_id);
        let trace_parent = incoming_trace_parent.map_or_else(
            || format!("00-{trace_id}-{}-01", generated_span_id()),
            ToOwned::to_owned,
        );
        let request_id = header(headers, REQUEST_ID_HEADER)
            .filter(|value| valid_id(value))
            .map_or_else(generated_id, ToOwned::to_owned);
        let correlation_id = header(headers, CORRELATION_ID_HEADER)
            .filter(|value| valid_id(value))
            .map_or_else(|| request_id.clone(), ToOwned::to_owned);
        let tenant_id = header(headers, TENANT_ID_HEADER)
            .filter(|value| valid_id(value))
            .map_or_else(String::new, ToOwned::to_owned);
        let command_id = header(headers, COMMAND_ID_HEADER)
            .filter(|value| valid_id(value))
            .map_or_else(String::new, ToOwned::to_owned);
        let trace_state = header(headers, TRACE_STATE_HEADER)
            .filter(|value| valid_trace_state(value))
            .map_or_else(String::new, ToOwned::to_owned);
        Self {
            request_id,
            correlation_id,
            tenant_id,
            command_id,
            trace_id,
            trace_parent,
            trace_state,
        }
    }

    fn write_response_headers(&self, headers: &mut HeaderMap) {
        insert(headers, REQUEST_ID_HEADER, &self.request_id);
        insert(headers, CORRELATION_ID_HEADER, &self.correlation_id);
        insert(headers, TRACE_ID_HEADER, &self.trace_id);
    }
}

tokio::task_local! {
    static ACTIVE_REQUEST_METADATA: RequestMetadata;
}

#[must_use]
pub fn current_request_metadata() -> Option<RequestMetadata> {
    ACTIVE_REQUEST_METADATA.try_with(Clone::clone).ok()
}

pub fn inject_tonic_metadata<T>(request: &mut tonic::Request<T>) {
    let Some(metadata) = current_request_metadata() else {
        return;
    };
    for (name, value) in [
        (REQUEST_ID_HEADER, metadata.request_id.as_str()),
        (CORRELATION_ID_HEADER, metadata.correlation_id.as_str()),
        (TENANT_ID_HEADER, metadata.tenant_id.as_str()),
        (COMMAND_ID_HEADER, metadata.command_id.as_str()),
        (TRACE_PARENT_HEADER, metadata.trace_parent.as_str()),
        (TRACE_STATE_HEADER, metadata.trace_state.as_str()),
    ] {
        if let Ok(value) = tonic::metadata::MetadataValue::try_from(value) {
            request.metadata_mut().insert(name, value);
        }
    }
}

#[derive(Clone, Debug)]
pub struct WorkloadAuthorization {
    certificate_fingerprints: Arc<Vec<[u8; 32]>>,
    methods: Arc<BTreeSet<String>>,
}

impl WorkloadAuthorization {
    /// Builds an exact certificate and RPC allowlist for an internal gRPC boundary.
    ///
    /// # Errors
    ///
    /// Returns an error when a fingerprint is not canonical lowercase SHA-256 hex, a
    /// fingerprint is duplicated, or an RPC path is empty, relative, or duplicated.
    pub fn try_new(
        certificate_sha256: &[String],
        methods: &[String],
    ) -> Result<Self, &'static str> {
        if certificate_sha256.is_empty() || methods.is_empty() {
            return Err("workload certificate fingerprints and methods are required");
        }
        let mut fingerprints = Vec::with_capacity(certificate_sha256.len());
        for encoded in certificate_sha256 {
            let fingerprint = decode_sha256(encoded)?;
            if fingerprints.contains(&fingerprint) {
                return Err("workload certificate fingerprint is duplicated");
            }
            fingerprints.push(fingerprint);
        }
        let mut allowed_methods = BTreeSet::new();
        for method in methods {
            if !method.starts_with('/')
                || method.len() < 3
                || !allowed_methods.insert(method.clone())
            {
                return Err("workload gRPC methods must be unique fully-qualified paths");
            }
        }
        Ok(Self {
            certificate_fingerprints: Arc::new(fingerprints),
            methods: Arc::new(allowed_methods),
        })
    }

    fn authorize<B>(&self, request: &Request<B>) -> Result<(), AuthorizationFailure> {
        if !self.methods.contains(request.uri().path()) {
            return Err(AuthorizationFailure::PermissionDenied);
        }
        let certificates = request
            .extensions()
            .get::<TlsConnectInfo<TcpConnectInfo>>()
            .and_then(TlsConnectInfo::peer_certs)
            .ok_or(AuthorizationFailure::Unauthenticated)?;
        let leaf = certificates
            .first()
            .ok_or(AuthorizationFailure::Unauthenticated)?;
        self.authorize_certificate(leaf.as_ref())
    }

    fn authorize_certificate(&self, certificate_der: &[u8]) -> Result<(), AuthorizationFailure> {
        let actual: [u8; 32] = Sha256::digest(certificate_der).into();
        if self
            .certificate_fingerprints
            .iter()
            .any(|expected| constant_time_eq(expected, &actual))
        {
            Ok(())
        } else {
            Err(AuthorizationFailure::PermissionDenied)
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum AuthorizationFailure {
    Unauthenticated,
    PermissionDenied,
}

#[derive(Clone, Debug)]
pub struct AdmissionLimiter {
    rate_per_second: f64,
    burst: f64,
    state: Arc<Mutex<AdmissionState>>,
}

#[derive(Debug)]
struct AdmissionState {
    tokens: f64,
    last_refill: Instant,
}

impl AdmissionLimiter {
    /// Builds a process-local token bucket for transport admission control.
    ///
    /// # Errors
    ///
    /// Returns an error when the rate or burst is zero.
    pub fn try_new(rate_per_second: u32, burst: u32) -> Result<Self, &'static str> {
        if rate_per_second == 0 || burst == 0 {
            return Err("gRPC admission rate and burst must be positive");
        }
        Ok(Self {
            rate_per_second: f64::from(rate_per_second),
            burst: f64::from(burst),
            state: Arc::new(Mutex::new(AdmissionState {
                tokens: f64::from(burst),
                last_refill: Instant::now(),
            })),
        })
    }

    fn admit(&self) -> bool {
        let Ok(mut state) = self.state.lock() else {
            return false;
        };
        let now = Instant::now();
        let elapsed = now.duration_since(state.last_refill).as_secs_f64();
        state.tokens = (state.tokens + elapsed * self.rate_per_second).min(self.burst);
        state.last_refill = now;
        if state.tokens < 1.0 {
            return false;
        }
        state.tokens -= 1.0;
        true
    }
}

#[derive(Clone)]
pub struct RequestMetadataLayer {
    service_name: &'static str,
    request_timeout: Option<Duration>,
    workload_authorization: Option<WorkloadAuthorization>,
    admission_limiter: Option<AdmissionLimiter>,
}

impl RequestMetadataLayer {
    #[must_use]
    pub const fn new(service_name: &'static str) -> Self {
        Self {
            service_name,
            request_timeout: None,
            workload_authorization: None,
            admission_limiter: None,
        }
    }

    #[must_use]
    pub const fn with_request_timeout(mut self, request_timeout: Duration) -> Self {
        self.request_timeout = Some(request_timeout);
        self
    }

    #[must_use]
    pub fn with_workload_authorization(
        mut self,
        workload_authorization: WorkloadAuthorization,
    ) -> Self {
        self.workload_authorization = Some(workload_authorization);
        self
    }

    #[must_use]
    pub fn with_admission_limiter(mut self, admission_limiter: AdmissionLimiter) -> Self {
        self.admission_limiter = Some(admission_limiter);
        self
    }
}

impl<S> Layer<S> for RequestMetadataLayer {
    type Service = RequestMetadataService<S>;

    fn layer(&self, inner: S) -> Self::Service {
        RequestMetadataService {
            inner,
            service_name: self.service_name,
            request_timeout: self.request_timeout,
            workload_authorization: self.workload_authorization.clone(),
            admission_limiter: self.admission_limiter.clone(),
        }
    }
}

#[derive(Clone)]
pub struct RequestMetadataService<S> {
    inner: S,
    service_name: &'static str,
    request_timeout: Option<Duration>,
    workload_authorization: Option<WorkloadAuthorization>,
    admission_limiter: Option<AdmissionLimiter>,
}

impl<S, RequestBody> Service<Request<RequestBody>> for RequestMetadataService<S>
where
    S: Service<Request<RequestBody>, Response = Response<tonic::body::Body>> + Send + 'static,
    S::Future: Send + 'static,
    S::Error: Send + 'static,
    RequestBody: Send + 'static,
{
    type Response = Response<tonic::body::Body>;
    type Error = S::Error;
    type Future = BoxFuture<'static, Result<Self::Response, Self::Error>>;

    fn poll_ready(&mut self, context: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(context)
    }

    fn call(&mut self, mut request: Request<RequestBody>) -> Self::Future {
        let started = Instant::now();
        let service_name = self.service_name;
        let request_timeout = self.request_timeout;
        let workload_authorization = self.workload_authorization.clone();
        let admission_limiter = self.admission_limiter.clone();
        let metadata = RequestMetadata::from_headers(request.headers());
        request.extensions_mut().insert(metadata.clone());
        let path = request.uri().path().to_owned();
        if let Some(authorization) = workload_authorization
            && let Err(failure) = authorization.authorize(&request)
        {
            let response = workload_denied_response(service_name, &path, &metadata, failure);
            return Box::pin(async move { Ok(response) });
        }
        if let Some(limiter) = admission_limiter
            && !limiter.admit()
        {
            let mut response = grpc_error_response("8", "transport admission limit exceeded");
            metadata.write_response_headers(response.headers_mut());
            return Box::pin(async move { Ok(response) });
        }
        let span = grpc_request_span(self.service_name, &path, &metadata);
        let future = self.inner.call(request);
        Box::pin(ACTIVE_REQUEST_METADATA.scope(metadata.clone(), async move {
            let guarded = AssertUnwindSafe(future.instrument(span)).catch_unwind();
            let outcome = if let Some(timeout) = request_timeout {
                if let Ok(outcome) = tokio::time::timeout(timeout, guarded).await {
                    outcome
                } else {
                    warn!(
                        service = service_name,
                        transport = "grpc",
                        rpc_method = %path,
                        request_id = %metadata.request_id,
                        correlation_id = %metadata.correlation_id,
                        trace_id = %metadata.trace_id,
                        duration_ms = elapsed_millis(started),
                        "gRPC request deadline exceeded"
                    );
                    let mut response = grpc_error_response("4", "request deadline exceeded");
                    metadata.write_response_headers(response.headers_mut());
                    return Ok(response);
                }
            } else {
                guarded.await
            };
            match outcome {
                Ok(Ok(mut response)) => {
                    metadata.write_response_headers(response.headers_mut());
                    let grpc_code = header(response.headers(), "grpc-status").unwrap_or("0");
                    info!(
                        service = service_name,
                        transport = "grpc",
                        rpc_method = %path,
                        request_id = %metadata.request_id,
                        correlation_id = %metadata.correlation_id,
                        tenant_id = %metadata.tenant_id,
                        command_id = %metadata.command_id,
                        trace_id = %metadata.trace_id,
                        grpc_code,
                        duration_ms = elapsed_millis(started),
                        "gRPC request completed"
                    );
                    Ok(response)
                }
                Ok(Err(error)) => {
                    warn!(
                        service = service_name,
                        transport = "grpc",
                        rpc_method = %path,
                        request_id = %metadata.request_id,
                        correlation_id = %metadata.correlation_id,
                        tenant_id = %metadata.tenant_id,
                        command_id = %metadata.command_id,
                        trace_id = %metadata.trace_id,
                        duration_ms = elapsed_millis(started),
                        "gRPC request failed at transport boundary"
                    );
                    Err(error)
                }
                Err(_) => {
                    warn!(
                        service = service_name,
                        transport = "grpc",
                        rpc_method = %path,
                        request_id = %metadata.request_id,
                        correlation_id = %metadata.correlation_id,
                        trace_id = %metadata.trace_id,
                        duration_ms = elapsed_millis(started),
                        "gRPC handler panic recovered"
                    );
                    let mut response = grpc_error_response("13", "internal server error");
                    metadata.write_response_headers(response.headers_mut());
                    Ok(response)
                }
            }
        }))
    }
}

fn elapsed_millis(started: Instant) -> u64 {
    u64::try_from(started.elapsed().as_millis()).unwrap_or(u64::MAX)
}

fn grpc_request_span(
    service_name: &'static str,
    path: &str,
    metadata: &RequestMetadata,
) -> tracing::Span {
    info_span!(
        "grpc.request",
        service = service_name,
        transport = "grpc",
        rpc_method = path,
        request_id = %metadata.request_id,
        correlation_id = %metadata.correlation_id,
        tenant_id = %metadata.tenant_id,
        command_id = %metadata.command_id,
        trace_id = %metadata.trace_id,
    )
}

fn workload_denied_response(
    service_name: &'static str,
    path: &str,
    metadata: &RequestMetadata,
    failure: AuthorizationFailure,
) -> Response<tonic::body::Body> {
    let (code, message) = match failure {
        AuthorizationFailure::Unauthenticated => {
            ("16", "verified workload certificate is required")
        }
        AuthorizationFailure::PermissionDenied => ("7", "workload is not authorized for this RPC"),
    };
    warn!(
        service = service_name,
        transport = "grpc",
        rpc_method = path,
        request_id = %metadata.request_id,
        correlation_id = %metadata.correlation_id,
        trace_id = %metadata.trace_id,
        grpc_code = code,
        "gRPC workload authorization denied"
    );
    let mut response = grpc_error_response(code, message);
    metadata.write_response_headers(response.headers_mut());
    response
}

fn decode_sha256(encoded: &str) -> Result<[u8; 32], &'static str> {
    if encoded.len() != 64
        || encoded
            .bytes()
            .any(|value| !value.is_ascii_digit() && !(b'a'..=b'f').contains(&value))
    {
        return Err("workload certificate fingerprint must be lowercase SHA-256 hex");
    }
    let mut decoded = [0_u8; 32];
    for (index, value) in decoded.iter_mut().enumerate() {
        let offset = index * 2;
        *value = (hex_nibble(encoded.as_bytes()[offset])? << 4)
            | hex_nibble(encoded.as_bytes()[offset + 1])?;
    }
    Ok(decoded)
}

fn hex_nibble(value: u8) -> Result<u8, &'static str> {
    match value {
        b'0'..=b'9' => Ok(value - b'0'),
        b'a'..=b'f' => Ok(value - b'a' + 10),
        _ => Err("workload certificate fingerprint contains invalid hexadecimal"),
    }
}

fn constant_time_eq(left: &[u8; 32], right: &[u8; 32]) -> bool {
    left.iter()
        .zip(right)
        .fold(0_u8, |difference, (left, right)| {
            difference | (left ^ right)
        })
        == 0
}

fn grpc_error_response(code: &'static str, message: &'static str) -> Response<tonic::body::Body> {
    let mut response = Response::new(tonic::body::Body::empty());
    *response.status_mut() = http::StatusCode::OK;
    response
        .headers_mut()
        .insert("content-type", HeaderValue::from_static("application/grpc"));
    response
        .headers_mut()
        .insert("grpc-status", HeaderValue::from_static(code));
    response
        .headers_mut()
        .insert("grpc-message", HeaderValue::from_static(message));
    response
}

fn header<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers.get(name)?.to_str().ok()
}

fn insert(headers: &mut HeaderMap, name: &'static str, value: &str) {
    if let Ok(value) = HeaderValue::from_str(value) {
        headers.insert(name, value);
    }
}

fn generated_id() -> String {
    Uuid::new_v4().simple().to_string()
}

fn generated_span_id() -> String {
    generated_id()[..16].to_owned()
}

fn valid_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|value| value.is_ascii_alphanumeric() || b"-_.:".contains(&value))
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
        || !version.bytes().all(is_lower_hex)
        || !trace_id.bytes().all(is_lower_hex)
        || !parent_id.bytes().all(is_lower_hex)
        || !flags.bytes().all(is_lower_hex)
    {
        return None;
    }
    Some(trace_id.to_owned())
}

fn valid_trace_state(value: &str) -> bool {
    value.len() <= 512 && value.bytes().all(|value| (0x20..=0x7e).contains(&value))
}

const fn is_lower_hex(value: u8) -> bool {
    value.is_ascii_digit() || (value >= b'a' && value <= b'f')
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::convert::Infallible;
    use std::fmt::Write as _;
    use tower::ServiceExt;

    fn lowercase_hex(bytes: &[u8]) -> String {
        bytes.iter().fold(
            String::with_capacity(bytes.len() * 2),
            |mut encoded, value| {
                let _ = write!(encoded, "{value:02x}");
                encoded
            },
        )
    }

    #[test]
    fn extracts_valid_w3c_trace_and_transport_ids() {
        let mut headers = HeaderMap::new();
        headers.insert(
            TRACE_PARENT_HEADER,
            HeaderValue::from_static("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"),
        );
        headers.insert(REQUEST_ID_HEADER, HeaderValue::from_static("request-1"));
        headers.insert(
            CORRELATION_ID_HEADER,
            HeaderValue::from_static("correlation-1"),
        );
        headers.insert(TENANT_ID_HEADER, HeaderValue::from_static("tenant-1"));
        headers.insert(COMMAND_ID_HEADER, HeaderValue::from_static("command-1"));
        let metadata = RequestMetadata::from_headers(&headers);
        assert_eq!(metadata.request_id, "request-1");
        assert_eq!(metadata.correlation_id, "correlation-1");
        assert_eq!(metadata.tenant_id, "tenant-1");
        assert_eq!(metadata.command_id, "command-1");
        assert_eq!(metadata.trace_id, "4bf92f3577b34da6a3ce929d0e0e4736");
    }

    #[test]
    fn rejects_invalid_identifiers_and_traceparent() {
        let mut headers = HeaderMap::new();
        headers.insert(REQUEST_ID_HEADER, HeaderValue::from_static("bad request"));
        headers.insert(TRACE_PARENT_HEADER, HeaderValue::from_static("invalid"));
        let metadata = RequestMetadata::from_headers(&headers);
        assert_eq!(metadata.request_id.len(), 32);
        assert_eq!(metadata.correlation_id, metadata.request_id);
        assert_eq!(metadata.trace_id.len(), 32);
    }

    #[test]
    fn workload_authorization_pins_certificate_and_canonical_configuration() {
        let certificate = b"client certificate";
        let digest = Sha256::digest(certificate);
        let encoded = lowercase_hex(&digest);
        let authorization = WorkloadAuthorization::try_new(
            std::slice::from_ref(&encoded),
            &["/bpmp.test.v1.Service/Call".to_owned()],
        )
        .expect("valid workload authorization");
        assert_eq!(authorization.authorize_certificate(certificate), Ok(()));
        assert_eq!(
            authorization.authorize_certificate(b"other certificate"),
            Err(AuthorizationFailure::PermissionDenied)
        );
        assert!(
            WorkloadAuthorization::try_new(
                &[encoded.to_uppercase()],
                &["/bpmp.test.v1.Service/Call".to_owned()]
            )
            .is_err()
        );
    }

    #[test]
    fn admission_limiter_is_bounded_and_fail_closed() {
        let limiter = AdmissionLimiter::try_new(1, 1).expect("valid admission limiter");
        assert!(limiter.admit());
        assert!(!limiter.admit());
        assert!(AdmissionLimiter::try_new(0, 1).is_err());
        assert!(AdmissionLimiter::try_new(1, 0).is_err());
    }

    #[tokio::test]
    async fn workload_authorization_fails_closed_without_mtls_peer() {
        let digest = Sha256::digest(b"client certificate");
        let encoded = lowercase_hex(&digest);
        let authorization =
            WorkloadAuthorization::try_new(&[encoded], &["/bpmp.test.v1.Service/Call".to_owned()])
                .expect("valid workload authorization");
        let request = Request::builder()
            .uri("/bpmp.test.v1.Service/Call")
            .body(())
            .expect("request");
        let response = RequestMetadataLayer::new("test")
            .with_workload_authorization(authorization)
            .layer(SlowService)
            .oneshot(request)
            .await
            .expect("infallible transport response");
        assert_eq!(header(response.headers(), "grpc-status"), Some("16"));
    }

    #[tokio::test]
    async fn forwards_canonical_metadata_to_downstream_tonic_calls() {
        let metadata = RequestMetadata {
            request_id: "request-1".to_owned(),
            correlation_id: "correlation-1".to_owned(),
            tenant_id: "tenant-1".to_owned(),
            command_id: "command-1".to_owned(),
            trace_id: "4bf92f3577b34da6a3ce929d0e0e4736".to_owned(),
            trace_parent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01".to_owned(),
            trace_state: "bpmp=test".to_owned(),
        };
        ACTIVE_REQUEST_METADATA
            .scope(metadata, async {
                let mut request = tonic::Request::new(());
                inject_tonic_metadata(&mut request);
                assert_eq!(
                    request.metadata().get(REQUEST_ID_HEADER).unwrap(),
                    "request-1"
                );
                assert_eq!(
                    request.metadata().get(TENANT_ID_HEADER).unwrap(),
                    "tenant-1"
                );
                assert_eq!(
                    request.metadata().get(COMMAND_ID_HEADER).unwrap(),
                    "command-1"
                );
                assert_eq!(
                    request.metadata().get(TRACE_STATE_HEADER).unwrap(),
                    "bpmp=test"
                );
            })
            .await;
    }

    #[derive(Clone)]
    struct PanicService;

    impl Service<Request<()>> for PanicService {
        type Response = Response<tonic::body::Body>;
        type Error = Infallible;
        type Future = BoxFuture<'static, Result<Self::Response, Self::Error>>;

        fn poll_ready(&mut self, _context: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }

        fn call(&mut self, _request: Request<()>) -> Self::Future {
            Box::pin(async { panic!("sensitive panic") })
        }
    }

    #[tokio::test]
    async fn recovers_handler_panic_as_internal_grpc_status() {
        let response = RequestMetadataLayer::new("test")
            .layer(PanicService)
            .oneshot(Request::new(()))
            .await
            .expect("infallible transport response");
        assert_eq!(header(response.headers(), "grpc-status"), Some("13"));
        assert!(header(response.headers(), REQUEST_ID_HEADER).is_some());
    }

    #[derive(Clone)]
    struct SlowService;

    impl Service<Request<()>> for SlowService {
        type Response = Response<tonic::body::Body>;
        type Error = Infallible;
        type Future = BoxFuture<'static, Result<Self::Response, Self::Error>>;

        fn poll_ready(&mut self, _context: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
            Poll::Ready(Ok(()))
        }

        fn call(&mut self, _request: Request<()>) -> Self::Future {
            Box::pin(async {
                tokio::time::sleep(Duration::from_secs(1)).await;
                Ok(Response::new(tonic::body::Body::empty()))
            })
        }
    }

    #[tokio::test]
    async fn enforces_configured_request_deadline() {
        let response = RequestMetadataLayer::new("test")
            .with_request_timeout(Duration::from_millis(5))
            .layer(SlowService)
            .oneshot(Request::new(()))
            .await
            .expect("infallible transport response");
        assert_eq!(header(response.headers(), "grpc-status"), Some("4"));
    }
}
