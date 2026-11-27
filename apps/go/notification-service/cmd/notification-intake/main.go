package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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
	configPath := flag.String("kafka-config", os.Getenv("NOTIFICATION_KAFKA_CONFIG"), "path to Kafka consumer JSON configuration")
	flag.Parse()

	if err := run(*configPath); err != nil {
		slog.Error("notification-intake stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	if configPath == "" {
		return errors.New("NOTIFICATION_KAFKA_CONFIG or -kafka-config is required")
	}
	rawCfg, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var kafkaCfg kafkaconfig.Consumer
	if err := json.Unmarshal(rawCfg, &kafkaCfg); err != nil {
		return err
	}
	if err := kafkaCfg.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt, err := bootstrap.InitRuntime(ctx, false)
	if err != nil {
		return err
	}
	defer rt.Close()

	client, err := newKafkaConsumer(kafkaCfg)
	if err != nil {
		return err
	}
	defer client.Close()

	slog.Info("notification-intake started", "topics", kafkaCfg.TopicList())
	for ctx.Err() == nil {
		fetches := client.PollRecords(ctx, kafkaCfg.BatchSize)
		if errs := fetches.Errors(); len(errs) > 0 {
			if ctx.Err() != nil {
				return nil
			}
			if err := kafkaconfig.CheckFetchErrors(ctx, errs); err != nil {
				return err
			}
		}
		for _, record := range fetches.Records() {
			if err := handleIntakeRecord(ctx, rt.Service, record); err != nil {
				return fmt.Errorf("intake record %s:%d:%d: %w", record.Topic, record.Partition, record.Offset, err)
			}
			if err := client.CommitRecords(ctx, record); err != nil {
				return fmt.Errorf("commit intake offset: %w", err)
			}
		}
	}
	return nil
}

func handleIntakeRecord(ctx context.Context, svc *application.Service, rec *kgo.Record) error {
	var cmd notificationv1.NotificationRequestCommand
	if err := proto.Unmarshal(rec.Value, &cmd); err != nil {
		return fmt.Errorf("decode NotificationRequestCommand: %w", err)
	}
	if cmd.GetTenantId() == "" || cmd.GetEventId() == "" || cmd.GetIdempotencyKey() == "" {
		return errors.New("NotificationRequestCommand missing tenant_id, event_id, or idempotency_key")
	}
	sum := sha256.Sum256(rec.Value)
	digest := hex.EncodeToString(sum[:])
	already, err := svc.Store().RecordInboxEvent(ctx, cmd.GetTenantId(), rec.Topic, cmd.GetEventId(), digest, time.Now().UTC())
	if err != nil || already {
		return err
	}

	recipients := make([]application.RecipientInput, 0, len(cmd.GetRecipients()))
	for _, r := range cmd.GetRecipients() {
		recipients = append(recipients, application.RecipientInput{
			SubjectRef:      r.GetSubjectRef(),
			Channel:         channelString(r.GetChannel()),
			ExternalAddress: r.GetExternalAddress(),
			DisplayName:     r.GetDisplayName(),
			Locale:          r.GetLocale(),
			Timezone:        r.GetTimezone(),
		})
	}
	var sendAt, expiresAt time.Time
	if cmd.GetSendAtEpochMs() > 0 {
		sendAt = time.UnixMilli(cmd.GetSendAtEpochMs()).UTC()
	}
	if cmd.GetExpiresAtEpochMs() > 0 {
		expiresAt = time.UnixMilli(cmd.GetExpiresAtEpochMs()).UTC()
	}
	_, err = svc.SubmitNotification(ctx, application.SubmitInput{
		TenantID:       cmd.GetTenantId(),
		ApplicationKey: cmd.GetApplicationKey(),
		IdempotencyKey: cmd.GetIdempotencyKey(),
		CorrelationID:  cmd.GetCorrelationId(),
		TemplateKey:    cmd.GetTemplateKey(),
		SchemaMajor:    cmd.GetSchemaMajor(),
		PinnedVersion:  cmd.GetPinnedVersion(),
		CategoryKey:    cmd.GetCategoryKey(),
		Priority:       int(cmd.GetPriority()),
		Locale:         cmd.GetLocale(),
		DataJSON:       cmd.GetDataJson(),
		Recipients:     recipients,
		SendAt:         sendAt,
		ExpiresAt:      expiresAt,
	})
	if errors.Is(err, application.ErrInvalid) || errors.Is(err, application.ErrNotFound) {
		slog.Warn("notification intake rejected invalid command", "event_id", cmd.GetEventId(), "error", err)
		return nil
	}
	return err
}

func newKafkaConsumer(c kafkaconfig.Consumer) (*kgo.Client, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(c.Brokers...),
		kgo.ClientID(c.ClientID),
		kgo.ConsumerGroup(c.ConsumerGroup),
		kgo.ConsumeTopics(c.TopicList()...),
		kgo.DialTimeout(c.DialTimeout()),
		kgo.RequestTimeoutOverhead(c.RequestTimeout()),
		kgo.FetchMaxBytes(int32(c.MaxMessageBytes)),
		kgo.DisableAutoCommit(),
	}
	if c.SecurityProtocol == kafkaconfig.ProtocolTLS {
		roots := x509.NewCertPool()
		raw, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(raw) {
			return nil, errors.New("Kafka CA contains no certificates")
		}
		key, err := tls.LoadX509KeyPair(c.CertificateFile, c.PrivateKeyFile)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      roots,
			Certificates: []tls.Certificate{key},
		}))
	}
	return kgo.NewClient(opts...)
}

func channelString(ch notificationv1.NotificationChannel) string {
	switch ch {
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_EMAIL:
		return "EMAIL"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_SMS:
		return "SMS"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_PUSH:
		return "PUSH"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_IN_APP:
		return "IN_APP"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_ZNS:
		return "ZNS"
	case notificationv1.NotificationChannel_NOTIFICATION_CHANNEL_WEBHOOK:
		return "WEBHOOK"
	default:
		return "EMAIL"
	}
}
