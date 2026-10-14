package kafkaconfig

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/twmb/franz-go/pkg/kgo"
	"os"
)

// NewProducerClient enforces acknowledgement/idempotence and shared TLS bootstrap.
func NewProducerClient(c Producer) (*kgo.Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	options := []kgo.Opt{kgo.SeedBrokers(c.Brokers...), kgo.ClientID(c.ClientID), kgo.DialTimeout(c.DialTimeout()), kgo.RequestTimeoutOverhead(c.RequestTimeout()), kgo.RequiredAcks(kgo.AllISRAcks()), kgo.MaxProduceRequestsInflightPerBroker(c.MaxInflight), kgo.ProducerBatchMaxBytes(int32(c.MaxMessageBytes)), kgo.ProduceRequestTimeout(c.MessageTimeout()), kgo.RecordDeliveryTimeout(c.MessageTimeout()), kgo.MaxBufferedRecords(1)}
	if c.SecurityProtocol == ProtocolTLS {
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
		options = append(options, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{key}}))
	}
	return kgo.NewClient(options...)
}
