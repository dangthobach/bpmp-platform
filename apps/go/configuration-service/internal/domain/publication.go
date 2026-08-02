package domain

import "time"

type Publication struct {
	EventID       string
	EventSequence uint64
	RequestID     string
	CorrelationID string
	CommandID     string
	TraceParent   string
	TraceState    string
	TenantID      string
	ProfileID     string
	VersionID     string
	ConfigVersion string
	PolicyVersion string
	Ordinal       uint64
	Owner         Owner
	Scope         Scope
	ContentHash   [32]byte
	Kind          string
	OccurredAt    time.Time
	AttemptCount  uint32
}

type TenantReadinessPublication struct {
	EventID        string
	EventSequence  uint64
	RequestID      string
	CorrelationID  string
	CommandID      string
	TraceParent    string
	TraceState     string
	TenantID       string
	TenantVersion  uint64
	Ready          bool
	MissingOwners  []Owner
	ProfileSetHash [32]byte
	OccurredAt     time.Time
	AttemptCount   uint32
}
