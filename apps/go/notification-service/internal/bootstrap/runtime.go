package bootstrap

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/postgres"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/queue"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
	"github.com/dangthobach/bpmp-platform/go/platform/actorcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Runtime struct {
	Service       *application.Service
	Pool          *pgxpool.Pool
	ProofVerifier *actorcontext.Verifier
}

func (r *Runtime) Close() {
	if r.Pool != nil {
		r.Pool.Close()
	}
}

func InitRuntime(ctx context.Context, requireActorProof bool) (*Runtime, error) {
	keyHex := strings.TrimSpace(os.Getenv("NOTIFICATION_PAYLOAD_KEY_HEX"))
	var rawKey []byte
	if keyHex != "" {
		decoded, err := hex.DecodeString(keyHex)
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("NOTIFICATION_PAYLOAD_KEY_HEX must be 64 hex chars (32 bytes)")
		}
		rawKey = decoded
	} else if keyFile := strings.TrimSpace(os.Getenv("NOTIFICATION_PAYLOAD_KEY_FILE")); keyFile != "" {
		data, err := os.ReadFile(keyFile)
		if err != nil || len(data) != 32 {
			return nil, errors.New("NOTIFICATION_PAYLOAD_KEY_FILE must contain 32 raw bytes")
		}
		rawKey = data
	} else {
		return nil, errors.New("NOTIFICATION_PAYLOAD_KEY_HEX or NOTIFICATION_PAYLOAD_KEY_FILE is required")
	}
	keyRef := strings.TrimSpace(os.Getenv("NOTIFICATION_PAYLOAD_KEY_REF"))
	if keyRef == "" {
		keyRef = "vault://transit/notification-payload-v1"
	}
	ring, err := crypto.NewKeyRing(keyRef, map[string][]byte{keyRef: rawKey}, rawKey)
	if err != nil {
		return nil, err
	}

	dsn := strings.TrimSpace(os.Getenv("NOTIFICATION_POSTGRES_DSN"))
	if dsn == "" {
		return nil, errors.New("NOTIFICATION_POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	store, err := postgres.New(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	pgQueue, err := queue.NewPostgresQueue(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}

	var allowedDomains []string
	if raw := strings.TrimSpace(os.Getenv("NOTIFICATION_TEST_ALLOWED_DOMAINS")); raw != "" {
		allowedDomains = strings.Split(raw, ",")
	}
	svc, err := application.NewService(
		store,
		pgQueue,
		ring,
		renderer.NewEngine(renderer.DefaultRenderLimits()),
		nil,
		application.Config{
			Environment:        os.Getenv("BPMP_ENV"),
			TestAllowedDomains: allowedDomains,
		},
	)
	if err != nil {
		pool.Close()
		return nil, err
	}

	var verifier *actorcontext.Verifier
	if requireActorProof {
		keyID := strings.TrimSpace(os.Getenv("NOTIFICATION_ACTOR_PROOF_KEY_ID"))
		workloadID := strings.TrimSpace(os.Getenv("NOTIFICATION_ACTOR_WORKLOAD_ID"))
		if workloadID == "" {
			workloadID = "notification-service"
		}
		pubPath := strings.TrimSpace(os.Getenv("NOTIFICATION_ACTOR_PROOF_PUBLIC_KEY"))
		pubBytes, err := os.ReadFile(pubPath)
		if err != nil || len(pubBytes) != ed25519.PublicKeySize || keyID == "" {
			pool.Close()
			return nil, errors.New("notification actor proof trust configuration is invalid")
		}
		verifier, err = actorcontext.New(actorcontext.Config{
			WorkloadID: workloadID,
			MaxBytes:   1 << 20,
			MaxRoles:   256,
			MaxCaps:    512,
			ClockSkew:  30 * time.Second,
			Keys:       map[string]ed25519.PublicKey{keyID: ed25519.PublicKey(pubBytes)},
		})
		if err != nil {
			pool.Close()
			return nil, err
		}
	}

	return &Runtime{
		Service:       svc,
		Pool:          pool,
		ProofVerifier: verifier,
	}, nil
}
