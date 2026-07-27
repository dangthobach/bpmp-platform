package domain

import "time"

type Publication struct {
	EventID       string
	EventSequence uint64
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
