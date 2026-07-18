//! Transport-level client.
//!
//! [`AuthzClient`] is the abstract port; [`HttpAuthzClient`] is the
//! reqwest-based adapter. A gRPC adapter can be added without changing
//! callers — both implement the same trait.

use std::time::Duration;

use async_trait::async_trait;
use reqwest::Client;

use crate::error::SdkError;
use crate::types::{
    CheckRequest, CheckResponse, ExplainRequest, ExplainResponse, FilterRequest, FilterResponse,
};

#[async_trait]
pub trait AuthzClient: Send + Sync {
    async fn check(&self, req: &CheckRequest) -> Result<CheckResponse, SdkError>;
    async fn filter(&self, req: &FilterRequest) -> Result<FilterResponse, SdkError>;
    async fn explain(&self, req: &ExplainRequest) -> Result<ExplainResponse, SdkError>;
}

#[derive(Debug, Clone)]
pub struct HttpAuthzClientConfig {
    /// Base URL, e.g. `https://authz.internal`.
    pub base_url: String,
    pub timeout: Duration,
    pub connect_timeout: Duration,
    /// `Authorization` header value used for service-to-service auth.
    /// Stored as bytes; never logged.
    pub auth_token: Option<String>,
}

impl Default for HttpAuthzClientConfig {
    fn default() -> Self {
        Self {
            base_url: "http://localhost:8080".to_owned(),
            timeout: Duration::from_millis(500),
            connect_timeout: Duration::from_millis(200),
            auth_token: None,
        }
    }
}

pub struct HttpAuthzClient {
    cfg: HttpAuthzClientConfig,
    http: Client,
}

impl HttpAuthzClient {
    pub fn new(cfg: HttpAuthzClientConfig) -> Result<Self, SdkError> {
        let mut builder = Client::builder()
            .timeout(cfg.timeout)
            .connect_timeout(cfg.connect_timeout)
            .pool_max_idle_per_host(32);
        if let Some(token) = &cfg.auth_token {
            let mut headers = reqwest::header::HeaderMap::new();
            let value = format!("Bearer {}", token);
            let mut hv = reqwest::header::HeaderValue::from_str(&value)
                .map_err(|e| SdkError::Config(e.to_string()))?;
            hv.set_sensitive(true);
            headers.insert(reqwest::header::AUTHORIZATION, hv);
            builder = builder.default_headers(headers);
        }
        let http = builder.build().map_err(SdkError::Transport)?;
        Ok(Self { cfg, http })
    }

    async fn post_json<Req: serde::Serialize, Res: serde::de::DeserializeOwned>(
        &self,
        path: &str,
        body: &Req,
    ) -> Result<Res, SdkError> {
        let url = format!("{}{}", self.cfg.base_url, path);
        let resp = self.http.post(&url).json(body).send().await?;
        let status = resp.status();
        let bytes = resp.bytes().await?;
        if !status.is_success() {
            let text = String::from_utf8_lossy(&bytes).to_string();
            return Err(SdkError::PdpError {
                status: status.as_u16(),
                code: "PDP_NON_2XX".to_owned(),
                message: text,
            });
        }
        let parsed: Res = serde_json::from_slice(&bytes)?;
        Ok(parsed)
    }
}

#[async_trait]
impl AuthzClient for HttpAuthzClient {
    #[tracing::instrument(skip_all, fields(action = %req.action, resource = %req.resource_type))]
    async fn check(&self, req: &CheckRequest) -> Result<CheckResponse, SdkError> {
        self.post_json("/authz/v1/check", req).await
    }

    #[tracing::instrument(skip_all, fields(action = %req.action, resource = %req.resource_type))]
    async fn filter(&self, req: &FilterRequest) -> Result<FilterResponse, SdkError> {
        self.post_json("/authz/v1/filter", req).await
    }

    #[tracing::instrument(skip_all, fields(action = %req.action, resource = %req.resource_type))]
    async fn explain(&self, req: &ExplainRequest) -> Result<ExplainResponse, SdkError> {
        self.post_json("/authz/v1/explain", req).await
    }
}
