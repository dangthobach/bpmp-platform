//! BPMP engine application boundary.
//!
//! Production storage, identity, crypto, and transport adapters are deliberately
//! absent from this initial slice. `memory` is suitable only for tests and local
//! development with synthetic data.

mod application;
mod authorization;
mod boundary_runtime;
mod event_codec;
pub mod memory;
mod outbox;
mod ports;
mod snapshot_codec;
mod transport;
mod wir_loader;

pub use application::{
    AuthorizationAudit, AuthorizedCommand, CommittedResult, EVENT_SCHEMA_VERSION, Engine,
    EngineError, EventEnvelope, EventMetadata, HandleOutcome, SnapshotEnvelope,
};
pub use authorization::EmbeddedAuthorizationProvider;
pub use boundary_runtime::{
    BoundaryCommandDispatcherPort, BoundaryDispatchCredentials, BoundaryDispatchCredentialsPort,
    BoundaryDispatchRequest, BoundaryDispatchSource, BoundaryEventSourcePort,
    BoundaryProjectionMutation, BoundaryProjectionRecord, BoundaryRuntime, BoundaryRuntimeError,
    BoundaryRuntimeStorePort, BoundarySignal, BoundarySignalKind, BoundarySubscriptionKey,
    ClaimedCorrelation, ClaimedTimer, ClockPort, DispatchOutcome, EngineBoundaryCommandDispatcher,
    OutboxBoundaryEventSource, ProjectedBoundarySubscription, ProjectionOutcome,
    SignalEnqueueOutcome, SystemClock, TimerDispatchCompletion, TimerSchedule,
    WorkflowDefinitionProviderPort,
};
pub use event_codec::{EventCodec, EventCodecError};
pub use outbox::{
    IntegrationEventPublisherPort, OutboxError, OutboxPublisher, OutboxPublisherConfig,
    OutboxRecord, OutboxStorePort, PublishAcknowledgement, PublishBatchOutcome, RetryDelayPort,
};
pub use ports::{
    ActorProofKind, AuthorizationError, AuthorizationProviderPort, AuthorizationRequest,
    AuthorizedPrincipal, CommitOutcome, CommitRequest, ConfigurationLookup,
    ConfigurationProviderPort, LoadedInstance, StoreError, WorkflowStorePort,
};
pub use snapshot_codec::{SNAPSHOT_SCHEMA_VERSION, SnapshotCodec, SnapshotCodecError};
pub use transport::{
    AuthoritativeCommandHandler, CommandDefinitionProviderPort, EngineCommandHandlerPort,
    GrpcEngineCommandService, GrpcTransportConfig, TransportError,
};
pub use wir_loader::{WirLoadError, WirLoader};
