package servermiddleware

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"strings"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func TestMTLSAuthorizerPinsVerifiedCertificateAndMethod(t *testing.T) {
	certificate := &x509.Certificate{Raw: []byte("certificate")}
	fingerprint := sha256.Sum256(certificate.Raw)
	authorizer, err := NewMTLSAuthorizer(MTLSConfig{
		AllowedCertificateSHA256: []string{hex.EncodeToString(fingerprint[:])},
		AllowedMethods:           []string{"/test.Service/Call"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{certificate},
			VerifiedChains:   [][]*x509.Certificate{{certificate}},
		},
	}})
	if err = authorizer.AuthorizeUnary(ctx, "/test.Service/Call", nil); err != nil {
		t.Fatal(err)
	}
	if err = authorizer.AuthorizeUnary(ctx, "/test.Service/Other", nil); err == nil {
		t.Fatal("unexpected method authorization")
	}
}

func TestMTLSAuthorizerFailsClosedWithoutVerifiedChain(t *testing.T) {
	fingerprint := sha256.Sum256([]byte("certificate"))
	authorizer, err := NewMTLSAuthorizer(MTLSConfig{
		AllowedCertificateSHA256: []string{hex.EncodeToString(fingerprint[:])},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = authorizer.AuthorizeUnary(context.Background(), "/test.Service/Call", nil); err == nil {
		t.Fatal("missing mTLS peer was authorized")
	}
}

func TestMTLSAuthorizerRejectsNonCanonicalFingerprint(t *testing.T) {
	fingerprint := sha256.Sum256([]byte("certificate"))
	_, err := NewMTLSAuthorizer(MTLSConfig{
		AllowedCertificateSHA256: []string{strings.ToUpper(hex.EncodeToString(fingerprint[:]))},
	})
	if err == nil {
		t.Fatal("uppercase certificate fingerprint was accepted")
	}
}
