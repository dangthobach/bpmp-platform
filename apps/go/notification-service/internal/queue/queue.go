package queue

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	LaneTransactional = "transactional"
	LaneNormal        = "normal"
	LaneBulk          = "bulk"

	StatusQueued    = "QUEUED"
	StatusSending   = "SENDING"
	StatusDeferred  = "DEFERRED"
	StatusCompleted = "COMPLETED"
	StatusFailed    = "FAILED"
)

var (
	ErrInvalidLane  = errors.New("dispatch queue lane must be transactional, normal, or bulk")
	ErrLeaseInvalid = errors.New("dispatch job lease is missing or expired")
	ErrJobNotFound  = errors.New("dispatch job not found")
)

type Job struct {
	ID          int64
	TenantID    uuid.UUID
	DeliveryID  uuid.UUID
	Lane        string
	Priority    int
	Status      string
	ScheduledAt time.Time
	LeaseUntil  *time.Time
	LeaseToken  uuid.UUID
	Attempts    int
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type DispatchQueue interface {
	Enqueue(ctx context.Context, tenantID, deliveryID uuid.UUID, lane string, scheduledAt time.Time) error
	EnqueueWithPriority(ctx context.Context, tenantID, deliveryID uuid.UUID, lane string, priority int, scheduledAt time.Time) error
	ClaimBatch(ctx context.Context, lane string, limit int, leaseDuration time.Duration, now time.Time) ([]Job, error)
	Complete(ctx context.Context, jobID int64, leaseToken uuid.UUID, now time.Time) error
	Snooze(ctx context.Context, jobID int64, leaseToken uuid.UUID, nextScheduledAt time.Time, lastError string, now time.Time) error
	Fail(ctx context.Context, jobID int64, leaseToken uuid.UUID, lastError string, now time.Time) error
}

func NormalizePriority(p int) int {
	if p < 1 || p > 3 {
		return 2
	}
	return p
}

func ValidLane(lane string) bool {
	return lane == LaneTransactional || lane == LaneNormal || lane == LaneBulk
}

type MemoryQueue struct {
	mu     sync.Mutex
	nextID int64
	jobs   []*Job
	byKey  map[string]int64
}

func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{
		byKey: make(map[string]int64),
	}
}

func jobKey(tenantID, deliveryID uuid.UUID) string {
	return tenantID.String() + ":" + deliveryID.String()
}

func (q *MemoryQueue) Enqueue(ctx context.Context, tenantID, deliveryID uuid.UUID, lane string, scheduledAt time.Time) error {
	return q.EnqueueWithPriority(ctx, tenantID, deliveryID, lane, 2, scheduledAt)
}

func (q *MemoryQueue) EnqueueWithPriority(_ context.Context, tenantID, deliveryID uuid.UUID, lane string, priority int, scheduledAt time.Time) error {
	if !ValidLane(lane) {
		return ErrInvalidLane
	}
	if tenantID == uuid.Nil || deliveryID == uuid.Nil {
		return errors.New("tenant_id and delivery_id are required")
	}
	priority = NormalizePriority(priority)
	q.mu.Lock()
	defer q.mu.Unlock()
	key := jobKey(tenantID, deliveryID)
	if existingID, exists := q.byKey[key]; exists {
		for _, j := range q.jobs {
			if j.ID == existingID {
				return nil
			}
		}
	}
	q.nextID++
	ts := scheduledAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	job := &Job{
		ID:          q.nextID,
		TenantID:    tenantID,
		DeliveryID:  deliveryID,
		Lane:        lane,
		Priority:    priority,
		Status:      StatusQueued,
		ScheduledAt: ts,
		CreatedAt:   ts,
		UpdatedAt:   ts,
	}
	q.jobs = append(q.jobs, job)
	q.byKey[key] = job.ID
	return nil
}

func (q *MemoryQueue) ClaimBatch(_ context.Context, lane string, limit int, leaseDuration time.Duration, now time.Time) ([]Job, error) {
	if !ValidLane(lane) {
		return nil, ErrInvalidLane
	}
	if limit <= 0 || leaseDuration <= 0 {
		return nil, errors.New("claim limit and lease duration must be positive")
	}
	nowUTC := now.UTC()
	until := nowUTC.Add(leaseDuration)
	q.mu.Lock()
	defer q.mu.Unlock()

	var ready []*Job
	for _, j := range q.jobs {
		if j.Lane != lane {
			continue
		}
		readyQueued := (j.Status == StatusQueued || j.Status == StatusDeferred) && !j.ScheduledAt.After(nowUTC)
		expiredLease := j.Status == StatusSending && j.LeaseUntil != nil && !j.LeaseUntil.After(nowUTC)
		if !readyQueued && !expiredLease {
			continue
		}
		ready = append(ready, j)
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority < ready[j].Priority
		}
		if !ready[i].ScheduledAt.Equal(ready[j].ScheduledAt) {
			return ready[i].ScheduledAt.Before(ready[j].ScheduledAt)
		}
		return ready[i].ID < ready[j].ID
	})

	claimed := make([]Job, 0, min(limit, len(ready)))
	for _, j := range ready {
		if len(claimed) >= limit {
			break
		}
		token := uuid.New()
		leaseCopy := until
		j.Status = StatusSending
		j.LeaseUntil = &leaseCopy
		j.LeaseToken = token
		j.Attempts++
		j.UpdatedAt = nowUTC
		claimed = append(claimed, cloneJob(*j))
	}
	return claimed, nil
}

func (q *MemoryQueue) Complete(_ context.Context, jobID int64, leaseToken uuid.UUID, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.findLeasedLocked(jobID, leaseToken, now.UTC())
	if err != nil {
		return err
	}
	j.Status = StatusCompleted
	j.LeaseUntil = nil
	j.LeaseToken = uuid.Nil
	j.UpdatedAt = now.UTC()
	return nil
}

func (q *MemoryQueue) Snooze(_ context.Context, jobID int64, leaseToken uuid.UUID, nextScheduledAt time.Time, lastError string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.findLeasedLocked(jobID, leaseToken, now.UTC())
	if err != nil {
		return err
	}
	j.Status = StatusDeferred
	j.ScheduledAt = nextScheduledAt.UTC()
	j.LeaseUntil = nil
	j.LeaseToken = uuid.Nil
	j.LastError = lastError
	j.UpdatedAt = now.UTC()
	return nil
}

func (q *MemoryQueue) Fail(_ context.Context, jobID int64, leaseToken uuid.UUID, lastError string, now time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.findLeasedLocked(jobID, leaseToken, now.UTC())
	if err != nil {
		return err
	}
	j.Status = StatusFailed
	j.LeaseUntil = nil
	j.LeaseToken = uuid.Nil
	j.LastError = lastError
	j.UpdatedAt = now.UTC()
	return nil
}

func (q *MemoryQueue) findLeasedLocked(jobID int64, leaseToken uuid.UUID, nowUTC time.Time) (*Job, error) {
	if leaseToken == uuid.Nil {
		return nil, ErrLeaseInvalid
	}
	for _, j := range q.jobs {
		if j.ID == jobID {
			if j.Status != StatusSending || j.LeaseToken != leaseToken || j.LeaseUntil == nil || j.LeaseUntil.Before(nowUTC) {
				return nil, ErrLeaseInvalid
			}
			return j, nil
		}
	}
	return nil, ErrJobNotFound
}

func cloneJob(j Job) Job {
	out := j
	if j.LeaseUntil != nil {
		cp := *j.LeaseUntil
		out.LeaseUntil = &cp
	}
	return out
}

type PostgresQueue struct {
	pool *pgxpool.Pool
}

func NewPostgresQueue(pool *pgxpool.Pool) (*PostgresQueue, error) {
	if pool == nil {
		return nil, errors.New("postgres pool is required")
	}
	return &PostgresQueue{pool: pool}, nil
}

func (q *PostgresQueue) Enqueue(ctx context.Context, tenantID, deliveryID uuid.UUID, lane string, scheduledAt time.Time) error {
	return enqueueExec(ctx, q.pool, tenantID, deliveryID, lane, 2, scheduledAt)
}

func (q *PostgresQueue) EnqueueWithPriority(ctx context.Context, tenantID, deliveryID uuid.UUID, lane string, priority int, scheduledAt time.Time) error {
	return enqueueExec(ctx, q.pool, tenantID, deliveryID, lane, priority, scheduledAt)
}

func EnqueueTx(ctx context.Context, tx pgx.Tx, tenantID, deliveryID uuid.UUID, lane string, scheduledAt time.Time) error {
	return enqueueExec(ctx, tx, tenantID, deliveryID, lane, 2, scheduledAt)
}

type pgExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func enqueueExec(ctx context.Context, exec pgExecer, tenantID, deliveryID uuid.UUID, lane string, priority int, scheduledAt time.Time) error {
	if !ValidLane(lane) {
		return ErrInvalidLane
	}
	if tenantID == uuid.Nil || deliveryID == uuid.Nil {
		return errors.New("tenant_id and delivery_id are required")
	}
	priority = NormalizePriority(priority)
	now := time.Now().UTC()
	sched := scheduledAt.UTC()
	if sched.IsZero() {
		sched = now
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO notification_dispatch_jobs (
			tenant_id, delivery_id, lane, priority, status, scheduled_at, attempts, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'QUEUED', $5, 0, $6, $6)
		ON CONFLICT (tenant_id, delivery_id) DO NOTHING
	`, tenantID, deliveryID, lane, priority, sched, now)
	return err
}

func (q *PostgresQueue) ClaimBatch(ctx context.Context, lane string, limit int, leaseDuration time.Duration, now time.Time) ([]Job, error) {
	if !ValidLane(lane) {
		return nil, ErrInvalidLane
	}
	if limit <= 0 || leaseDuration <= 0 {
		return nil, errors.New("claim limit and lease duration must be positive")
	}
	nowUTC := now.UTC()
	until := nowUTC.Add(leaseDuration)
	token := uuid.New()
	rows, err := q.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM notification_dispatch_jobs
			WHERE lane = $1
			  AND (
			    (status IN ('QUEUED', 'DEFERRED') AND scheduled_at <= $2)
			    OR (status = 'SENDING' AND lease_until IS NOT NULL AND lease_until <= $2)
			  )
			ORDER BY priority ASC, scheduled_at ASC, id ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		UPDATE notification_dispatch_jobs j
		SET status = 'SENDING',
		    lease_until = $4,
		    lease_token = $5,
		    attempts = j.attempts + 1,
		    updated_at = $2
		FROM candidates c
		WHERE j.id = c.id
		RETURNING j.id, j.tenant_id, j.delivery_id, j.lane, j.priority, j.status,
		          j.scheduled_at, j.lease_until, j.lease_token, j.attempts,
		          COALESCE(j.last_error, ''), j.created_at, j.updated_at
	`, lane, nowUTC, limit, until, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID, &j.TenantID, &j.DeliveryID, &j.Lane, &j.Priority, &j.Status,
			&j.ScheduledAt, &j.LeaseUntil, &j.LeaseToken, &j.Attempts,
			&j.LastError, &j.CreatedAt, &j.UpdatedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (q *PostgresQueue) Complete(ctx context.Context, jobID int64, leaseToken uuid.UUID, now time.Time) error {
	nowUTC := now.UTC()
	tag, err := q.pool.Exec(ctx, `
		UPDATE notification_dispatch_jobs
		SET status = 'COMPLETED', lease_until = NULL, lease_token = NULL, updated_at = $3
		WHERE id = $1 AND status = 'SENDING' AND lease_token = $2 AND lease_until >= $3
	`, jobID, leaseToken, nowUTC)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseInvalid
	}
	return nil
}

func (q *PostgresQueue) Snooze(ctx context.Context, jobID int64, leaseToken uuid.UUID, nextScheduledAt time.Time, lastError string, now time.Time) error {
	nowUTC := now.UTC()
	tag, err := q.pool.Exec(ctx, `
		UPDATE notification_dispatch_jobs
		SET status = 'DEFERRED', scheduled_at = $4, lease_until = NULL, lease_token = NULL, last_error = $5, updated_at = $3
		WHERE id = $1 AND status = 'SENDING' AND lease_token = $2 AND lease_until >= $3
	`, jobID, leaseToken, nowUTC, nextScheduledAt.UTC(), lastError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseInvalid
	}
	return nil
}

func (q *PostgresQueue) Fail(ctx context.Context, jobID int64, leaseToken uuid.UUID, lastError string, now time.Time) error {
	nowUTC := now.UTC()
	tag, err := q.pool.Exec(ctx, `
		UPDATE notification_dispatch_jobs
		SET status = 'FAILED', lease_until = NULL, lease_token = NULL, last_error = $4, updated_at = $3
		WHERE id = $1 AND status = 'SENDING' AND lease_token = $2 AND lease_until >= $3
	`, jobID, leaseToken, nowUTC, lastError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseInvalid
	}
	return nil
}
