package kafkaconfig

import "testing"

func TestCanonicalNames(t *testing.T) {
	t.Parallel()
	valid := []string{
		"bpmp.configuration.publications.v1.production",
		"bpmp.engine.committed-events.v1.e2e",
		"bpmp.engine.configuration-reloader.v1.e2e",
	}
	for _, value := range valid {
		if err := ValidateTopic(value); err != nil {
			t.Fatalf("expected canonical name %q to pass: %v", value, err)
		}
	}
	invalid := []string{
		"configuration.publications",
		"bpmp.configuration.publications.e2e",
		"bpmp.configuration.publications.v1.E2E",
		"bpmp_configuration_publications_v1_e2e",
	}
	for _, value := range invalid {
		if err := ValidateTopic(value); err == nil {
			t.Fatalf("expected non-canonical name %q to fail", value)
		}
	}
}

func TestProducerRequiresIdempotenceAndAllAcknowledgements(t *testing.T) {
	t.Parallel()
	config := Producer{
		Bootstrap: Bootstrap{
			Brokers: []string{"kafka-1:9093"}, ClientID: "bpmp-configuration-publisher",
			SecurityProtocol: ProtocolPlaintext, DialTimeoutMS: 1000, RequestTimeoutMS: 3000,
		},
		Topic: "bpmp.configuration.publications.v1.e2e", MessageTimeoutMS: 5000,
		MaxInflight: 1, MaxMessageBytes: 1048576, RequiredAcks: "ALL", EnableIdempotence: true,
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("expected valid producer configuration: %v", err)
	}
	config.EnableIdempotence = false
	if err := config.Validate(); err == nil {
		t.Fatal("expected non-idempotent producer to fail closed")
	}
}

func TestBootstrapRejectsInvalidOrDuplicateBrokerPorts(t *testing.T) {
	t.Parallel()
	config := Bootstrap{
		Brokers:          []string{"kafka:9092", "kafka:9092"},
		ClientID:         "bpmp-configuration-publisher",
		SecurityProtocol: ProtocolPlaintext,
		DialTimeoutMS:    1000,
		RequestTimeoutMS: 1000,
	}
	if config.Validate() == nil {
		t.Fatal("duplicate broker addresses must be rejected")
	}
	config.Brokers = []string{"kafka:not-a-port"}
	if config.Validate() == nil {
		t.Fatal("non-numeric broker ports must be rejected")
	}
}

func TestProducerAndConsumerBoundsAreFinite(t *testing.T) {
	t.Parallel()
	producer := Producer{
		Bootstrap: Bootstrap{
			Brokers: []string{"kafka:9092"}, ClientID: "bpmp-configuration-publisher",
			SecurityProtocol: ProtocolPlaintext, DialTimeoutMS: 1000, RequestTimeoutMS: 1000,
		},
		Topic: "bpmp.configuration.publications.v1.e2e", MessageTimeoutMS: 1000,
		MaxInflight: 6, MaxMessageBytes: 1024, RequiredAcks: "ALL", EnableIdempotence: true,
	}
	if producer.Validate() == nil {
		t.Fatal("idempotent producer inflight bound must be enforced")
	}
	consumer := Consumer{
		Bootstrap:     producer.Bootstrap,
		Topic:         "bpmp.configuration.publications.v1.e2e",
		ConsumerGroup: "bpmp.engine.configuration-reloader.v1.e2e",
		BatchSize:     1001, MaxMessageBytes: 1024, PollTimeoutMS: 1000, SessionTimeoutMS: 2000,
	}
	if consumer.Validate() == nil {
		t.Fatal("consumer batch bound must be enforced")
	}
}
