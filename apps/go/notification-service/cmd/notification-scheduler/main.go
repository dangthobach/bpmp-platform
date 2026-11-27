package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/application"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/bootstrap"
	notificationv1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/notification/v1"
	"github.com/dangthobach/bpmp-platform/go/platform/kafkaconfig"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

func main() {
	intervalMS := flag.Int64("interval-ms", 1000, "scheduler & outbox relay interval in milliseconds")
	producerCfgPath := flag.String("kafka-producer-config", os.Getenv("NOTIFICATION_OUTBOX_KAFKA_CONFIG"), "optional path to Kafka producer JSON config")
	flag.Parse()

	if err := run(time.Duration(*intervalMS)*time.Millisecond, *producerCfgPath); err != nil {
		slog.Error("notification-scheduler stopped", "error", err)
		os.Exit(1)
	}
}

func run(interval time.Duration, producerCfgPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap.InitRuntime(ctx, false)
	if err != nil {
		return err
	}
	defer rt.Close()

	var kafkaClient *kgo.Client
	if strings.TrimSpace(producerCfgPath) != "" {
		raw, err := os.ReadFile(producerCfgPath)
		if err != nil {
			return err
		}
		var prodCfg kafkaconfig.Producer
		if err := json.Unmarshal(raw, &prodCfg); err != nil {
			return err
		}
		kafkaClient, err = kafkaconfig.NewProducerClient(prodCfg)
		if err != nil {
			return err
		}
		defer kafkaClient.Close()
	}

	deliveryTopic := os.Getenv("NOTIFICATION_DELIVERY_EVENTS_TOPIC")
	inappTopic := os.Getenv("NOTIFICATION_INAPP_HINTS_TOPIC")
	invalidationTopic := os.Getenv("NOTIFICATION_INVALIDATIONS_TOPIC")

	slog.Info("notification-scheduler started", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		now := time.Now().UTC()
		if _, err := rt.Service.Store().ActivateDueScheduledReleases(ctx, now); err != nil {
			slog.Error("activate scheduled template releases", "error", err)
		}
		if kafkaClient != nil {
			if err := relayOutbox(ctx, rt.Service.Store(), kafkaClient, deliveryTopic, inappTopic, invalidationTopic, now); err != nil {
				slog.Error("relay notification outbox", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func relayOutbox(
	ctx context.Context,
	store application.Store,
	client *kgo.Client,
	deliveryTopic, inappTopic, invalidationTopic string,
	now time.Time,
) error {
	pending, err := store.ClaimPendingOutbox(ctx, 100)
	if err != nil {
		return err
	}
	for _, item := range pending {
		topic, key, val, ok, err := encodeOutboxEvent(item, deliveryTopic, inappTopic, invalidationTopic)
		if err != nil {
			return err
		}
		if ok {
			res := client.ProduceSync(ctx, &kgo.Record{Topic: topic, Key: []byte(key), Value: val})
			if err := res.FirstErr(); err != nil {
				return err
			}
		}
		if err := store.MarkOutboxPublished(ctx, item.TenantID, item.ID, now); err != nil {
			return err
		}
	}
	return nil
}

func encodeOutboxEvent(item application.OutboxMessage, deliveryTopic, inappTopic, invalidationTopic string) (string, string, []byte, bool, error) {
	switch item.EventType {
	case "notification.inapp_hint":
		if inappTopic == "" {
			return "", "", nil, false, nil
		}
		var m struct {
			EventID             string `json:"event_id"`
			TenantID            string `json:"tenant_id"`
			RecipientSubjectRef string `json:"recipient_subject_ref"`
			DeliveryID          string `json:"delivery_id"`
			MessageID           string `json:"message_id"`
			CategoryKey         string `json:"category_key"`
			Sequence            uint64 `json:"sequence"`
			OccurredAtEpochMS   int64  `json:"occurred_at_epoch_ms"`
		}
		if err := json.Unmarshal(item.PayloadJSON, &m); err != nil {
			return "", "", nil, false, err
		}
		raw, err := proto.Marshal(&notificationv1.InAppNotificationHint{
			SchemaVersion:       1,
			EventId:             m.EventID,
			TenantId:            m.TenantID,
			RecipientSubjectRef: m.RecipientSubjectRef,
			DeliveryId:          m.DeliveryID,
			MessageId:           m.MessageID,
			CategoryKey:         m.CategoryKey,
			Sequence:            m.Sequence,
			OccurredAtEpochMs:   m.OccurredAtEpochMS,
		})
		return inappTopic, m.TenantID + ":" + m.RecipientSubjectRef, raw, true, err

	case "notification.delivery_event":
		if deliveryTopic == "" {
			return "", "", nil, false, nil
		}
		var m struct {
			EventID           string `json:"event_id"`
			TenantID          string `json:"tenant_id"`
			RequestID         string `json:"request_id"`
			DeliveryID        string `json:"delivery_id"`
			ApplicationID     string `json:"application_id"`
			TemplateKey       string `json:"template_key"`
			SchemaMajor       uint32 `json:"schema_major"`
			TemplateVersionID string `json:"template_version_id"`
			CorrelationID     string `json:"correlation_id"`
			ErrorCode         string `json:"error_code"`
			OccurredAtEpochMS int64  `json:"occurred_at_epoch_ms"`
		}
		if err := json.Unmarshal(item.PayloadJSON, &m); err != nil {
			return "", "", nil, false, err
		}
		raw, err := proto.Marshal(&notificationv1.NotificationDeliveryEvent{
			SchemaVersion:     1,
			EventId:           m.EventID,
			TenantId:          m.TenantID,
			RequestId:         m.RequestID,
			DeliveryId:        m.DeliveryID,
			ApplicationId:     m.ApplicationID,
			TemplateKey:       m.TemplateKey,
			SchemaMajor:       m.SchemaMajor,
			TemplateVersionId: m.TemplateVersionID,
			CorrelationId:     m.CorrelationID,
			ErrorCode:         m.ErrorCode,
			OccurredAtEpochMs: m.OccurredAtEpochMS,
		})
		return deliveryTopic, m.TenantID + ":" + m.RequestID, raw, true, err

	case "notification.invalidation":
		if invalidationTopic == "" {
			return "", "", nil, false, nil
		}
		var m struct {
			EventID           string `json:"event_id"`
			TenantID          string `json:"tenant_id"`
			ArtifactID        string `json:"artifact_id"`
			Ordinal           uint64 `json:"ordinal"`
			ExpiresAtEpochMS  int64  `json:"expires_at_epoch_ms"`
			OccurredAtEpochMS int64  `json:"occurred_at_epoch_ms"`
		}
		if err := json.Unmarshal(item.PayloadJSON, &m); err != nil {
			return "", "", nil, false, err
		}
		raw, err := proto.Marshal(&notificationv1.NotificationCacheInvalidationEvent{
			SchemaVersion:     1,
			EventId:           m.EventID,
			TenantId:          m.TenantID,
			ArtifactId:        m.ArtifactID,
			Ordinal:           m.Ordinal,
			ExpiresAtEpochMs:  m.ExpiresAtEpochMS,
			OccurredAtEpochMs: m.OccurredAtEpochMS,
		})
		return invalidationTopic, m.TenantID, raw, true, err
	}
	return "", "", nil, false, nil
}
