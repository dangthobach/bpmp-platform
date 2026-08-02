use std::pin::Pin;
use std::sync::Arc;
use std::time::Duration;

use bpmp_contracts::engine::v1::remote_worker_dispatch_service_server::{
    RemoteWorkerDispatchService, RemoteWorkerDispatchServiceServer,
};
use bpmp_contracts::engine::v1::{EngineToWorker, WorkerToEngine, worker_to_engine};
use tokio::sync::mpsc;
use tokio_stream::Stream;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Request, Response, Status, Streaming};

#[derive(Debug, Clone, Copy, Eq, PartialEq)]
pub struct RemoteWorkerStreamLimits {
    pub channel_capacity: usize,
    pub registration_timeout: Duration,
}

#[allow(clippy::missing_errors_doc)]
pub trait RemoteWorkerStreamHandlerPort: Send + Sync + 'static {
    fn stream_limits(&self) -> Result<RemoteWorkerStreamLimits, RemoteWorkerTransportError>;

    fn register(
        &self,
        peer_certificate_der: &[u8],
        frame: WorkerToEngine,
        outbound: mpsc::Sender<Result<EngineToWorker, Status>>,
    ) -> Result<(), RemoteWorkerTransportError>;

    fn handle_frame(&self, frame: WorkerToEngine) -> Result<(), RemoteWorkerTransportError>;

    fn disconnect(&self, session_id: &str);
}

impl<T: RemoteWorkerStreamHandlerPort + ?Sized> RemoteWorkerStreamHandlerPort for Arc<T> {
    fn stream_limits(&self) -> Result<RemoteWorkerStreamLimits, RemoteWorkerTransportError> {
        (**self).stream_limits()
    }

    fn register(
        &self,
        peer_certificate_der: &[u8],
        frame: WorkerToEngine,
        outbound: mpsc::Sender<Result<EngineToWorker, Status>>,
    ) -> Result<(), RemoteWorkerTransportError> {
        (**self).register(peer_certificate_der, frame, outbound)
    }

    fn handle_frame(&self, frame: WorkerToEngine) -> Result<(), RemoteWorkerTransportError> {
        (**self).handle_frame(frame)
    }

    fn disconnect(&self, session_id: &str) {
        (**self).disconnect(session_id);
    }
}

pub struct GrpcRemoteWorkerDispatchService<H> {
    handler: Arc<H>,
}

impl<H> GrpcRemoteWorkerDispatchService<H> {
    pub fn new(handler: H) -> Self {
        Self {
            handler: Arc::new(handler),
        }
    }

    pub fn into_server(
        self,
        max_decoding_bytes: usize,
        max_encoding_bytes: usize,
    ) -> RemoteWorkerDispatchServiceServer<Self>
    where
        H: RemoteWorkerStreamHandlerPort,
    {
        RemoteWorkerDispatchServiceServer::new(self)
            .max_decoding_message_size(max_decoding_bytes)
            .max_encoding_message_size(max_encoding_bytes)
    }
}

type DispatchStream = Pin<Box<dyn Stream<Item = Result<EngineToWorker, Status>> + Send + 'static>>;

#[tonic::async_trait]
impl<H> RemoteWorkerDispatchService for GrpcRemoteWorkerDispatchService<H>
where
    H: RemoteWorkerStreamHandlerPort,
{
    type OpenDispatchStreamStream = DispatchStream;

    async fn open_dispatch_stream(
        &self,
        request: Request<Streaming<WorkerToEngine>>,
    ) -> Result<Response<Self::OpenDispatchStreamStream>, Status> {
        let limits = self.handler.stream_limits().map_err(Status::from)?;
        if limits.channel_capacity == 0 || limits.registration_timeout.is_zero() {
            return Err(Status::internal(
                RemoteWorkerTransportError::InvalidConfiguration.to_string(),
            ));
        }
        let peer_certificates = request
            .peer_certs()
            .ok_or_else(|| Status::unauthenticated("remote worker mTLS certificate is missing"))?;
        let peer_certificate = peer_certificates
            .first()
            .ok_or_else(|| Status::unauthenticated("remote worker certificate chain is empty"))?
            .as_ref()
            .to_vec();
        let mut inbound = request.into_inner();
        let first = tokio::time::timeout(limits.registration_timeout, inbound.message())
            .await
            .map_err(|_| Status::deadline_exceeded("remote worker registration timed out"))?
            .map_err(|error| Status::invalid_argument(error.to_string()))?
            .ok_or_else(|| Status::invalid_argument("remote worker stream is empty"))?;
        if !matches!(first.frame, Some(worker_to_engine::Frame::Registration(_))) {
            return Err(Status::failed_precondition(
                "the first remote worker frame must be registration",
            ));
        }
        let session_id = first.session_id.clone();
        let (sender, receiver) = mpsc::channel(limits.channel_capacity);
        let handler = Arc::clone(&self.handler);
        tokio::task::spawn_blocking(move || handler.register(&peer_certificate, first, sender))
            .await
            .map_err(|error| Status::internal(format!("registration worker failed: {error}")))?
            .map_err(Status::from)?;

        let handler = Arc::clone(&self.handler);
        tokio::spawn(async move {
            while let Ok(Some(frame)) = inbound.message().await {
                if frame.session_id != session_id {
                    break;
                }
                let frame_handler = Arc::clone(&handler);
                let frame_result =
                    tokio::task::spawn_blocking(move || frame_handler.handle_frame(frame)).await;
                if !matches!(frame_result, Ok(Ok(()))) {
                    break;
                }
            }
            handler.disconnect(&session_id);
        });

        Ok(Response::new(Box::pin(ReceiverStream::new(receiver))))
    }
}

#[derive(Debug, thiserror::Error, Clone, Eq, PartialEq)]
pub enum RemoteWorkerTransportError {
    #[error("remote worker transport configuration is invalid")]
    InvalidConfiguration,
    #[error("remote worker authentication failed")]
    Unauthenticated,
    #[error("remote worker frame is invalid: {0}")]
    InvalidFrame(String),
    #[error("remote worker stream is backpressured")]
    Backpressured,
    #[error("remote worker session failed: {0}")]
    Session(String),
}

impl From<RemoteWorkerTransportError> for Status {
    fn from(value: RemoteWorkerTransportError) -> Self {
        match value {
            RemoteWorkerTransportError::Unauthenticated => Self::unauthenticated(value.to_string()),
            RemoteWorkerTransportError::InvalidConfiguration => Self::internal(value.to_string()),
            RemoteWorkerTransportError::InvalidFrame(_) => {
                Self::invalid_argument(value.to_string())
            }
            RemoteWorkerTransportError::Backpressured => {
                Self::resource_exhausted(value.to_string())
            }
            RemoteWorkerTransportError::Session(_) => Self::failed_precondition(value.to_string()),
        }
    }
}
