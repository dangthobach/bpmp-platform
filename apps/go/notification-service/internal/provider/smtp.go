package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const smtpConfigSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["host", "port", "from_address"],
  "properties": {
    "host": {"type": "string", "minLength": 1},
    "port": {"type": "integer", "minimum": 1, "maximum": 65535},
    "tls_mode": {"type": "string", "enum": ["STARTTLS", "IMPLICIT_TLS", "NONE"]},
    "allow_insecure_local": {"type": "boolean"},
    "auth_mode": {"type": "string", "enum": ["NONE", "PLAIN"]},
    "username": {"type": "string"},
    "from_address": {"type": "string", "minLength": 3},
    "from_name": {"type": "string"},
    "reply_to": {"type": "string"},
    "timeout_ms": {"type": "integer", "minimum": 100, "maximum": 60000}
  },
  "additionalProperties": false
}`

type SMTPConfig struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	TLSMode            string `json:"tls_mode"`
	AllowInsecureLocal bool   `json:"allow_insecure_local"`
	AuthMode           string `json:"auth_mode"`
	Username           string `json:"username"`
	FromAddress        string `json:"from_address"`
	FromName           string `json:"from_name"`
	ReplyTo            string `json:"reply_to"`
	TimeoutMS          int    `json:"timeout_ms"`
}

type SMTPDriver struct {
	TLSConfigOverride *tls.Config
}

func NewSMTPDriver() *SMTPDriver {
	return &SMTPDriver{}
}

func (d *SMTPDriver) Kind() string { return "smtp" }

func (d *SMTPDriver) ConfigSchema() []byte { return []byte(smtpConfigSchema) }

func (d *SMTPDriver) Open(ctx context.Context, rev ProviderRevision, secrets SecretResolver) (Sender, error) {
	var cfg SMTPConfig
	dec := json.NewDecoder(bytes.NewReader(rev.ConfigJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid smtp config: %w", err)
	}
	if strings.TrimSpace(cfg.Host) == "" || cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, errors.New("smtp host and valid port are required")
	}
	if cfg.TLSMode == "" {
		cfg.TLSMode = "STARTTLS"
	}
	if cfg.TLSMode != "STARTTLS" && cfg.TLSMode != "IMPLICIT_TLS" && cfg.TLSMode != "NONE" {
		return nil, errors.New("unsupported smtp tls_mode")
	}
	if cfg.TLSMode == "NONE" && !cfg.AllowInsecureLocal {
		return nil, errors.New("smtp tls_mode NONE requires allow_insecure_local")
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "NONE"
	}
	if cfg.AuthMode != "NONE" && cfg.AuthMode != "PLAIN" {
		return nil, errors.New("unsupported smtp auth_mode")
	}
	if err := validateMailAddress(cfg.FromAddress); err != nil {
		return nil, fmt.Errorf("invalid from_address: %w", err)
	}
	if err := rejectCRLF("from_name", cfg.FromName); err != nil {
		return nil, err
	}
	if cfg.ReplyTo != "" {
		if err := validateMailAddress(cfg.ReplyTo); err != nil {
			return nil, fmt.Errorf("invalid reply_to: %w", err)
		}
	}
	var password string
	if cfg.AuthMode == "PLAIN" {
		if strings.TrimSpace(cfg.Username) == "" {
			return nil, errors.New("smtp PLAIN auth requires username")
		}
		if secrets != nil && rev.CredentialsRef != "" {
			secret, err := secrets.ResolveSecret(ctx, rev.CredentialsRef)
			if err != nil {
				return nil, fmt.Errorf("resolve smtp credentials: %w", err)
			}
			password = secret
		}
	}
	timeout := 10 * time.Second
	if cfg.TimeoutMS > 0 {
		timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	}
	return &smtpSender{
		cfg:         cfg,
		password:    password,
		timeout:     timeout,
		tlsOverride: d.TLSConfigOverride,
	}, nil
}

type smtpSender struct {
	cfg         SMTPConfig
	password    string
	timeout     time.Duration
	tlsOverride *tls.Config
}

func (s *smtpSender) Close() error { return nil }

func (s *smtpSender) Send(ctx context.Context, msg RenderedMessage) (SendResult, error) {
	started := time.Now()
	fromAddr := s.cfg.FromAddress
	if msg.FromAddress != "" {
		fromAddr = msg.FromAddress
	}
	fromName := s.cfg.FromName
	if msg.FromName != "" {
		fromName = msg.FromName
	}
	replyTo := s.cfg.ReplyTo
	if msg.ReplyTo != "" {
		replyTo = msg.ReplyTo
	}

	rawMIME, messageID, buildErr := BuildMIMEMessage(MIMEMessageInput{
		FromAddress:    fromAddr,
		FromName:       fromName,
		ToAddress:      msg.RecipientAddress,
		ReplyTo:        replyTo,
		Subject:        msg.Subject,
		TextBody:       msg.TextBody,
		HTMLBody:       msg.HTMLBody,
		UnsubscribeURL: msg.UnsubscribeURL,
		CorrelationID:  msg.CorrelationID,
	})
	if buildErr != nil {
		return SendResult{
			Outcome:   OutcomePermanent,
			ErrorCode: "INVALID_EMAIL_HEADER_OR_ADDRESS",
			Latency:   time.Since(started),
		}, buildErr
	}

	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	sendCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(sendCtx, "tcp", addr)
	if err != nil {
		return SendResult{
			Outcome:   OutcomeRetryable,
			ErrorCode: "SMTP_DIAL_FAILED",
			Latency:   time.Since(started),
		}, err
	}
	defer conn.Close()
	if deadline, ok := sendCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: s.cfg.Host,
	}
	if s.tlsOverride != nil {
		tlsCfg = s.tlsOverride.Clone()
		if tlsCfg.ServerName == "" {
			tlsCfg.ServerName = s.cfg.Host
		}
	}

	var client *smtp.Client
	if s.cfg.TLSMode == "IMPLICIT_TLS" {
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(sendCtx); err != nil {
			return SendResult{
				Outcome:   OutcomeRetryable,
				ErrorCode: "SMTP_TLS_HANDSHAKE_FAILED",
				Latency:   time.Since(started),
			}, err
		}
		client, err = smtp.NewClient(tlsConn, s.cfg.Host)
	} else {
		client, err = smtp.NewClient(conn, s.cfg.Host)
	}
	if err != nil {
		return SendResult{
			Outcome:   OutcomeRetryable,
			ErrorCode: "SMTP_GREETING_FAILED",
			Latency:   time.Since(started),
		}, err
	}
	defer client.Close()

	if s.cfg.TLSMode == "STARTTLS" {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return SendResult{
				Outcome:   OutcomeRetryable,
				ErrorCode: "SMTP_STARTTLS_UNSUPPORTED",
				Latency:   time.Since(started),
			}, errors.New("server does not advertise STARTTLS")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return SendResult{
				Outcome:   OutcomeRetryable,
				ErrorCode: "SMTP_STARTTLS_FAILED",
				Latency:   time.Since(started),
			}, err
		}
	}

	if s.cfg.AuthMode == "PLAIN" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return SendResult{
				Outcome:   OutcomeAuthFailed,
				ErrorCode: "SMTP_AUTH_FAILED",
				Latency:   time.Since(started),
			}, err
		}
	}

	if err := client.Mail(fromAddr); err != nil {
		return classifySMTPError(err, false, started), err
	}
	if err := client.Rcpt(msg.RecipientAddress); err != nil {
		return classifySMTPError(err, false, started), err
	}
	writer, err := client.Data()
	if err != nil {
		return classifySMTPError(err, false, started), err
	}
	if _, err := writer.Write(rawMIME); err != nil {
		return classifySMTPError(err, true, started), err
	}
	if err := writer.Close(); err != nil {
		return classifySMTPError(err, true, started), err
	}
	_ = client.Quit()
	return SendResult{
		Outcome:            OutcomeOK,
		ProviderMessageID:  messageID,
		ProviderStatusCode: "250",
		Latency:            time.Since(started),
	}, nil
}

func classifySMTPError(err error, dataPhase bool, started time.Time) SendResult {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		outcome := OutcomeRetryable
		code := "SMTP_TIMEOUT"
		if dataPhase {
			outcome = OutcomeAmbiguous
			code = "SMTP_DATA_TIMEOUT_AMBIGUOUS"
		}
		return SendResult{Outcome: outcome, ErrorCode: code, Latency: time.Since(started)}
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "5") {
		if strings.HasPrefix(msg, "530") || strings.HasPrefix(msg, "535") {
			return SendResult{Outcome: OutcomeAuthFailed, ProviderStatusCode: msg[:3], ErrorCode: "SMTP_5XX_AUTH", Latency: time.Since(started)}
		}
		return SendResult{Outcome: OutcomePermanent, ProviderStatusCode: msg[:3], ErrorCode: "SMTP_5XX_PERMANENT", Latency: time.Since(started)}
	}
	if strings.HasPrefix(msg, "421") || strings.HasPrefix(msg, "450") || strings.HasPrefix(msg, "451") || strings.HasPrefix(msg, "452") {
		outcome := OutcomeRetryable
		if strings.Contains(strings.ToLower(msg), "rate") || strings.Contains(strings.ToLower(msg), "throttl") {
			outcome = OutcomeThrottled
		}
		return SendResult{Outcome: outcome, ProviderStatusCode: msg[:3], ErrorCode: "SMTP_4XX_TEMP", Latency: time.Since(started)}
	}
	if dataPhase {
		return SendResult{Outcome: OutcomeAmbiguous, ErrorCode: "SMTP_DATA_ERROR_AMBIGUOUS", Latency: time.Since(started)}
	}
	return SendResult{Outcome: OutcomeRetryable, ErrorCode: "SMTP_TRANSIENT_ERROR", Latency: time.Since(started)}
}

type MIMEMessageInput struct {
	FromAddress    string
	FromName       string
	ToAddress      string
	ReplyTo        string
	Subject        string
	TextBody       string
	HTMLBody       string
	UnsubscribeURL string
	CorrelationID  string
}

// BuildMIMEMessage constructs an RFC 5322 / RFC 8058 MIME message while strictly
// rejecting CRLF or NUL characters in every header and address field.
func BuildMIMEMessage(in MIMEMessageInput) ([]byte, string, error) {
	if err := validateMailAddress(in.FromAddress); err != nil {
		return nil, "", fmt.Errorf("from: %w", err)
	}
	if err := validateMailAddress(in.ToAddress); err != nil {
		return nil, "", fmt.Errorf("to: %w", err)
	}
	if in.ReplyTo != "" {
		if err := validateMailAddress(in.ReplyTo); err != nil {
			return nil, "", fmt.Errorf("reply_to: %w", err)
		}
	}
	for field, val := range map[string]string{
		"from_name":       in.FromName,
		"subject":         in.Subject,
		"unsubscribe_url": in.UnsubscribeURL,
		"correlation_id":  in.CorrelationID,
	} {
		if err := rejectCRLF(field, val); err != nil {
			return nil, "", err
		}
	}
	if in.UnsubscribeURL != "" {
		parsed, err := url.Parse(in.UnsubscribeURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || strings.ContainsAny(in.UnsubscribeURL, "<> \t\r\n") {
			return nil, "", errors.New("List-Unsubscribe URL must be a clean https:// URL")
		}
	}

	msgID := fmt.Sprintf("<%s@bpmp.notification>", uuid.NewString())
	fromHeader := (&mail.Address{Name: in.FromName, Address: in.FromAddress}).String()
	toHeader := (&mail.Address{Address: in.ToAddress}).String()
	encodedSubject := mime.QEncoding.Encode("utf-8", in.Subject)

	var buf bytes.Buffer
	writeHeader := func(k, v string) error {
		if err := rejectCRLF(k, v); err != nil {
			return err
		}
		buf.WriteString(k)
		buf.WriteString(": ")
		buf.WriteString(v)
		buf.WriteString("\r\n")
		return nil
	}

	for _, kv := range [][2]string{
		{"From", fromHeader},
		{"To", toHeader},
		{"Subject", encodedSubject},
		{"Message-ID", msgID},
		{"Date", time.Now().UTC().Format(time.RFC1123Z)},
		{"MIME-Version", "1.0"},
	} {
		if err := writeHeader(kv[0], kv[1]); err != nil {
			return nil, "", err
		}
	}
	if in.ReplyTo != "" {
		if err := writeHeader("Reply-To", (&mail.Address{Address: in.ReplyTo}).String()); err != nil {
			return nil, "", err
		}
	}
	if in.CorrelationID != "" {
		if err := writeHeader("X-Correlation-ID", in.CorrelationID); err != nil {
			return nil, "", err
		}
	}
	if in.UnsubscribeURL != "" {
		if err := writeHeader("List-Unsubscribe", "<"+in.UnsubscribeURL+">"); err != nil {
			return nil, "", err
		}
		if err := writeHeader("List-Unsubscribe-Post", "List-Unsubscribe=One-Click"); err != nil {
			return nil, "", err
		}
	}

	boundary := "bpmp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if in.HTMLBody != "" && in.TextBody != "" {
		if err := writeHeader("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary)); err != nil {
			return nil, "", err
		}
		buf.WriteString("\r\n")
		writePart(&buf, boundary, "text/plain; charset=utf-8", in.TextBody)
		writePart(&buf, boundary, "text/html; charset=utf-8", in.HTMLBody)
		buf.WriteString("--" + boundary + "--\r\n")
	} else if in.HTMLBody != "" {
		_ = writeHeader("Content-Type", "text/html; charset=utf-8")
		_ = writeHeader("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		writeQuotedPrintable(&buf, in.HTMLBody)
		buf.WriteString("\r\n")
	} else {
		_ = writeHeader("Content-Type", "text/plain; charset=utf-8")
		_ = writeHeader("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		writeQuotedPrintable(&buf, in.TextBody)
		buf.WriteString("\r\n")
	}
	return buf.Bytes(), msgID, nil
}

func writePart(buf *bytes.Buffer, boundary, contentType, body string) {
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: " + contentType + "\r\n")
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	writeQuotedPrintable(buf, body)
	buf.WriteString("\r\n")
}

func writeQuotedPrintable(buf *bytes.Buffer, body string) {
	qw := quotedprintable.NewWriter(buf)
	_, _ = qw.Write([]byte(body))
	_ = qw.Close()
}

func rejectCRLF(field, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("header %s contains forbidden CR/LF/NUL characters", field)
	}
	return nil
}

func validateMailAddress(raw string) error {
	if err := rejectCRLF("address", raw); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errors.New("mail address is empty")
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed.Address != trimmed {
		return errors.New("mail address must be a bare RFC 5322 addr-spec")
	}
	if strings.ContainsAny(parsed.Address, "\r\n\x00") {
		return errors.New("mail address contains control characters")
	}
	return nil
}
