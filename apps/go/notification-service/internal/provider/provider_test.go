package provider

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPRejectsCRLFInjectionAndDeliversValidMail(t *testing.T) {
	// 1. CRLF injection in Subject, From, To, and RFC 8058 List-Unsubscribe must fail closed!
	injectionCases := map[string]MIMEMessageInput{
		"subject_crlf": {
			FromAddress: "noreply@bpmp.local", ToAddress: "user@example.com",
			Subject: "Hello\r\nBcc: attacker@evil.com", TextBody: "hi",
		},
		"to_crlf": {
			FromAddress: "noreply@bpmp.local", ToAddress: "user@example.com\r\nRcpt To:<evil@evil.com>",
			Subject: "Hello", TextBody: "hi",
		},
		"unsubscribe_crlf": {
			FromAddress: "noreply@bpmp.local", ToAddress: "user@example.com",
			Subject: "Hello", TextBody: "hi",
			UnsubscribeURL: "https://bpmp.local/unsub\r\nX-Injected: 1",
		},
		"unsubscribe_non_https": {
			FromAddress: "noreply@bpmp.local", ToAddress: "user@example.com",
			Subject: "Hello", TextBody: "hi",
			UnsubscribeURL: "http://bpmp.local/unsub",
		},
	}
	for name, in := range injectionCases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := BuildMIMEMessage(in); err == nil {
				t.Fatalf("expected CRLF/scheme guard to reject %s", name)
			}
		})
	}

	// 2. Real loopback SMTP server test (Mailpit local mode).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	receivedData := make(chan string, 1)
	go serveMinimalSMTP(ln, receivedData)

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	drv := NewSMTPDriver()
	sender, err := drv.Open(context.Background(), ProviderRevision{
		ProviderID: "prov-mailpit",
		DriverKind: "smtp",
		ConfigJSON: []byte(fmt.Sprintf(`{
			"host":"127.0.0.1",
			"port":%s,
			"tls_mode":"NONE",
			"allow_insecure_local":true,
			"from_address":"noreply@bpmp.local",
			"from_name":"BPMP Notify"
		}`, portStr)),
	}, nil)
	if err != nil {
		t.Fatalf("open smtp driver: %v", err)
	}
	defer sender.Close()

	res, err := sender.Send(context.Background(), RenderedMessage{
		RecipientAddress: "alice@example.com",
		Subject:          "Thông báo duyệt hồ sơ",
		TextBody:         "Xin chào Alice",
		HTMLBody:         "<p>Xin chào <b>Alice</b></p>",
		UnsubscribeURL:   "https://portal.bpmp.local/unsub/123",
		CorrelationID:    "corr-99",
	})
	if err != nil || res.Outcome != OutcomeOK {
		t.Fatalf("smtp send failed: res=%+v err=%v", res, err)
	}
	raw := <-receivedData
	if !strings.Contains(raw, "List-Unsubscribe: <https://portal.bpmp.local/unsub/123>") ||
		!strings.Contains(raw, "List-Unsubscribe-Post: List-Unsubscribe=One-Click") {
		t.Fatalf("missing RFC 8058 List-Unsubscribe headers in MIME:\n%s", raw)
	}
}

func serveMinimalSMTP(ln net.Listener, out chan<- string) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	_, _ = fmt.Fprintf(conn, "220 localhost ESMTP Mailpit\r\n")
	var dataBuf strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" || line == ".\n" {
				inData = false
				out <- dataBuf.String()
				_, _ = fmt.Fprintf(conn, "250 2.0.0 Ok: queued\r\n")
				continue
			}
			dataBuf.WriteString(line)
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			_, _ = fmt.Fprintf(conn, "250-localhost\r\n250 SIZE 10485760\r\n")
		case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
			_, _ = fmt.Fprintf(conn, "250 2.1.0 Ok\r\n")
		case upper == "DATA":
			inData = true
			_, _ = fmt.Fprintf(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
		case upper == "QUIT":
			_, _ = fmt.Fprintf(conn, "221 Bye\r\n")
			return
		default:
			_, _ = fmt.Fprintf(conn, "250 Ok\r\n")
		}
	}
}

func TestRouterFailoverAmbiguousPolicyAndBreakerOverrides(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	clock := now
	breakers := NewBreakerRegistry(BreakerPolicy{
		RollingWindow:           time.Minute,
		MinRequests:             2,
		FailureRateThresholdBps: 5000,
		OpenStateDuration:       30 * time.Second,
		HalfOpenMaxProbes:       1,
		MaxManualOverrideTTL:    time.Hour,
	})
	router := NewRouter([]Driver{NewMockDriver()}, breakers, nil, func() time.Time { return clock })

	primaryFail := RouteCandidate{
		ProviderID: "prov-primary",
		Weight:     100,
		Revision: ProviderRevision{
			ProviderID: "prov-primary", RevisionID: "rev-1", DriverKind: "mock",
			ConfigJSON: []byte(`{"forced_outcome":"RETRYABLE"}`),
		},
	}
	backupZNS := RouteCandidate{
		ProviderID: "prov-backup",
		Weight:     100,
		Revision: ProviderRevision{
			ProviderID: "prov-backup", RevisionID: "rev-2", DriverKind: "mock",
			PresetKey: "zalo-zns-mock", ConfigJSON: []byte(`{"preset":"zalo-zns-mock"}`),
		},
	}
	tiers := []RouteTier{{Candidates: []RouteCandidate{primaryFail}}, {Candidates: []RouteCandidate{backupZNS}}}

	// 1. RETRYABLE on primary fails over to backup Zalo ZNS mock!
	res := router.Dispatch(context.Background(), tiers, AmbiguousReconcileBeforeRetry, 1, RenderedMessage{
		TenantID: "t1", Channel: "ZNS", ProviderTemplateID: "zns_otp_001",
	})
	if res.Outcome != DispatchSent || res.SelectedProviderID != "prov-backup" || len(res.Attempts) != 2 {
		t.Fatalf("expected failover to prov-backup, got %+v", res)
	}

	// 2. PERMANENT error NEVER fails over (protects backup sender reputation) and does NOT trip breaker!
	permCand := RouteCandidate{
		ProviderID: "prov-healthy",
		Revision: ProviderRevision{
			ProviderID: "prov-healthy", RevisionID: "rev-h", DriverKind: "mock",
			ConfigJSON: []byte(`{"forced_outcome":"PERMANENT"}`),
		},
	}
	for i := 0; i < 5; i++ {
		permRes := router.Dispatch(context.Background(), []RouteTier{{Candidates: []RouteCandidate{permCand}}, {Candidates: []RouteCandidate{backupZNS}}}, AmbiguousResendOnFailover, 1, RenderedMessage{
			TenantID: "t1", Channel: "ZNS", ProviderTemplateID: "zns_otp_001",
		})
		if permRes.Outcome != DispatchFailedPermanent || len(permRes.Attempts) != 1 {
			t.Fatalf("PERMANENT outcome must not fail over: %+v", permRes)
		}
	}
	if snap := breakers.Snapshot("t1", "prov-healthy", clock); snap.BreakerState != BreakerClosed {
		t.Fatalf("PERMANENT errors must be excluded from tripping breaker, got %+v", snap)
	}

	// 3. AMBIGUOUS outcome respects category policy (RECONCILE_BEFORE_RETRY stops failover; RESEND_ON_FAILOVER fails over).
	ambigCand := RouteCandidate{
		ProviderID: "prov-ambig",
		Revision: ProviderRevision{
			ProviderID: "prov-ambig", RevisionID: "rev-a", DriverKind: "mock",
			ConfigJSON: []byte(`{"forced_outcome":"AMBIGUOUS"}`),
		},
	}
	ambigTiers := []RouteTier{{Candidates: []RouteCandidate{ambigCand}}, {Candidates: []RouteCandidate{backupZNS}}}
	reconcileRes := router.Dispatch(context.Background(), ambigTiers, AmbiguousReconcileBeforeRetry, 1, RenderedMessage{
		TenantID: "t1", Channel: "ZNS", ProviderTemplateID: "zns_otp_001",
	})
	if reconcileRes.Outcome != DispatchReconcileRequired || len(reconcileRes.Attempts) != 1 {
		t.Fatalf("expected RECONCILE_BEFORE_RETRY to halt failover, got %+v", reconcileRes)
	}
	resendRes := router.Dispatch(context.Background(), ambigTiers, AmbiguousResendOnFailover, 1, RenderedMessage{
		TenantID: "t1", Channel: "ZNS", ProviderTemplateID: "zns_otp_001",
	})
	if resendRes.Outcome != DispatchSent || resendRes.SelectedProviderID != "prov-backup" {
		t.Fatalf("expected RESEND_ON_FAILOVER to fail over to backup, got %+v", resendRes)
	}

	// 4. Manual FORCE_OPEN override blocks traffic and expires automatically after TTL!
	if _, err := breakers.SetOverride("t1", "prov-backup", OverrideForceOpen, "maintenance drill", 5*time.Minute, 1, clock); err != nil {
		t.Fatal(err)
	}
	blockedRes := router.Dispatch(context.Background(), []RouteTier{{Candidates: []RouteCandidate{backupZNS}}}, AmbiguousResendOnFailover, 1, RenderedMessage{
		TenantID: "t1", Channel: "ZNS", ProviderTemplateID: "zns_otp_001",
	})
	if blockedRes.Outcome != DispatchDeferred {
		t.Fatalf("expected DispatchDeferred when all providers are FORCE_OPEN, got %+v", blockedRes)
	}
	clock = clock.Add(6 * time.Minute)
	recoveredRes := router.Dispatch(context.Background(), []RouteTier{{Candidates: []RouteCandidate{backupZNS}}}, AmbiguousResendOnFailover, 1, RenderedMessage{
		TenantID: "t1", Channel: "ZNS", ProviderTemplateID: "zns_otp_001",
	})
	if recoveredRes.Outcome != DispatchSent {
		t.Fatalf("expected override TTL to expire and allow traffic, got %+v", recoveredRes)
	}
}
