//! Telemetry bootstrap: structured logs + (optional) OTLP traces + Prometheus.

use anyhow::Result;
use metrics_exporter_prometheus::PrometheusBuilder;
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt, EnvFilter};

pub struct TelemetryGuard {
    /// Prometheus recorder handle — exposed via `/metrics` route if needed.
    pub prometheus_handle: metrics_exporter_prometheus::PrometheusHandle,
}

pub fn init(service_name: &str, otlp_endpoint: Option<&str>) -> Result<TelemetryGuard> {
    // ── Logs ────────────────────────────────────────────────────────────────
    let filter = EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info"));
    let fmt_layer = tracing_subscriber::fmt::layer().json();

    let registry = tracing_subscriber::registry().with(filter).with(fmt_layer);

    // ── Traces (optional OTLP) ──────────────────────────────────────────────
    if let Some(endpoint) = otlp_endpoint {
        use opentelemetry::KeyValue;
        use opentelemetry_otlp::WithExportConfig;
        use opentelemetry_sdk::{trace, Resource};

        let tracer =
            opentelemetry_otlp::new_pipeline()
                .tracing()
                .with_exporter(
                    opentelemetry_otlp::new_exporter()
                        .tonic()
                        .with_endpoint(endpoint),
                )
                .with_trace_config(trace::config().with_resource(Resource::new(vec![
                    KeyValue::new("service.name", service_name.to_owned()),
                ])))
                .install_batch(opentelemetry_sdk::runtime::Tokio)?;

        let otel_layer = tracing_opentelemetry::layer().with_tracer(tracer);
        registry.with(otel_layer).try_init()?;
    } else {
        registry.try_init()?;
    }

    // ── Metrics ────────────────────────────────────────────────────────────
    let prometheus_handle = PrometheusBuilder::new()
        .install_recorder()
        .map_err(|e| anyhow::anyhow!("prometheus install failed: {e}"))?;

    Ok(TelemetryGuard { prometheus_handle })
}
