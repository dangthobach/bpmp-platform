use std::sync::Arc;

use bpmp_contracts::engine::v1::engine_history_service_server::{
    EngineHistoryService, EngineHistoryServiceServer,
};
use bpmp_contracts::engine::v1::{
    ReadHistoryPageRequest, ReadHistoryPageResponse, StoredWorkflowEvent,
};
use bpmp_domain_core::{InstanceId, TenantId};
use bpmp_transport_observability::WorkloadAuthorization;
use tonic::{Request, Response, Status};

use crate::{EventCodec, HistoryReadPort};

const HISTORY_METHOD: &str = "/bpmp.engine.v1.EngineHistoryService/ReadHistoryPage";

#[derive(Clone)]
pub struct GrpcEngineHistoryService<S> {
    store: Arc<S>,
    authorization: WorkloadAuthorization,
}

impl<S> GrpcEngineHistoryService<S>
where
    S: HistoryReadPort + 'static,
{
    #[must_use]
    pub fn new(store: Arc<S>, authorization: WorkloadAuthorization) -> Self {
        Self {
            store,
            authorization,
        }
    }

    #[must_use]
    pub fn into_server(
        self,
        max_decoding_bytes: usize,
        max_encoding_bytes: usize,
    ) -> EngineHistoryServiceServer<Self> {
        EngineHistoryServiceServer::new(self)
            .max_decoding_message_size(max_decoding_bytes)
            .max_encoding_message_size(max_encoding_bytes)
    }
}

#[tonic::async_trait]
impl<S> EngineHistoryService for GrpcEngineHistoryService<S>
where
    S: HistoryReadPort + 'static,
{
    async fn read_history_page(
        &self,
        request: Request<ReadHistoryPageRequest>,
    ) -> Result<Response<ReadHistoryPageResponse>, Status> {
        if !self
            .authorization
            .is_tonic_request_authorized(&request, HISTORY_METHOD)
        {
            return Err(Status::permission_denied(
                "workload is not authorized for Engine history",
            ));
        }
        let request = request.into_inner();
        if !(1..=100).contains(&request.page_size) {
            return Err(Status::invalid_argument(
                "page_size must be between 1 and 100",
            ));
        }
        let tenant_id = TenantId::new(request.tenant_id)
            .map_err(|_| Status::invalid_argument("tenant_id is invalid"))?;
        let instance_id = InstanceId::new(request.instance_id)
            .map_err(|_| Status::invalid_argument("instance_id is invalid"))?;
        let page = self
            .store
            .read_history_page(
                &tenant_id,
                &instance_id,
                request.after_sequence,
                request.page_size as usize,
            )
            .map_err(|_| Status::unavailable("Engine history is unavailable"))?;
        let events = page
            .events
            .iter()
            .map(|event| StoredWorkflowEvent {
                sequence: event.metadata.sequence,
                event_envelope: EventCodec::encode(event),
            })
            .collect();
        Ok(Response::new(ReadHistoryPageResponse {
            events,
            watermark: page.watermark,
            next_after_sequence: page.next_after_sequence.unwrap_or_default(),
            has_more: page.next_after_sequence.is_some(),
        }))
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicBool, Ordering};

    use super::*;
    use crate::{HistoryPage, StoreError};
    use bpmp_domain_core::{InstanceId, TenantId};

    struct ReadTracker(AtomicBool);

    impl HistoryReadPort for ReadTracker {
        fn read_history_page(
            &self,
            _: &TenantId,
            _: &InstanceId,
            _: u64,
            _: usize,
        ) -> Result<HistoryPage, StoreError> {
            self.0.store(true, Ordering::SeqCst);
            Ok(HistoryPage {
                events: Vec::new(),
                watermark: 0,
                next_after_sequence: None,
            })
        }
    }

    #[tokio::test]
    async fn denies_unverified_workload_before_reading_event_store() {
        let authorization =
            WorkloadAuthorization::try_new(&["ab".repeat(32)], &[HISTORY_METHOD.to_owned()])
                .unwrap();
        let tracker = Arc::new(ReadTracker(AtomicBool::new(false)));
        let service = GrpcEngineHistoryService::new(tracker.clone(), authorization);
        let result = service
            .read_history_page(Request::new(ReadHistoryPageRequest {
                tenant_id: "tenant-a".into(),
                instance_id: "instance-a".into(),
                after_sequence: 0,
                page_size: 10,
            }))
            .await;
        assert_eq!(result.unwrap_err().code(), tonic::Code::PermissionDenied);
        assert!(!tracker.0.load(Ordering::SeqCst));
    }
}
