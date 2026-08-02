package servermiddleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

type MTLSConfig struct {
	AllowedCertificateSHA256 []string
	AllowedMethods           []string
}

type MTLSAuthorizer struct {
	fingerprints [][sha256.Size]byte
	methods      map[string]struct{}
}

func NewMTLSAuthorizer(config MTLSConfig) (*MTLSAuthorizer, error) {
	if len(config.AllowedCertificateSHA256) == 0 {
		return nil, errors.New("at least one client certificate fingerprint is required")
	}
	fingerprints := make([][sha256.Size]byte, 0, len(config.AllowedCertificateSHA256))
	seen := make(map[[sha256.Size]byte]struct{}, len(config.AllowedCertificateSHA256))
	for _, encoded := range config.AllowedCertificateSHA256 {
		if encoded != strings.ToLower(encoded) {
			return nil, errors.New("client certificate fingerprint must be lowercase SHA-256 hex")
		}
		decoded, err := hex.DecodeString(encoded)
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("client certificate fingerprint must be lowercase SHA-256 hex")
		}
		var fingerprint [sha256.Size]byte
		copy(fingerprint[:], decoded)
		if _, duplicate := seen[fingerprint]; duplicate {
			return nil, errors.New("client certificate fingerprint is duplicated")
		}
		seen[fingerprint] = struct{}{}
		fingerprints = append(fingerprints, fingerprint)
	}
	methods := make(map[string]struct{}, len(config.AllowedMethods))
	for _, method := range config.AllowedMethods {
		if method == "" || method[0] != '/' {
			return nil, errors.New("authorized gRPC method must be fully qualified")
		}
		if _, duplicate := methods[method]; duplicate {
			return nil, errors.New("authorized gRPC method is duplicated")
		}
		methods[method] = struct{}{}
	}
	return &MTLSAuthorizer{fingerprints: fingerprints, methods: methods}, nil
}

func (a *MTLSAuthorizer) AuthorizeUnary(ctx context.Context, method string, _ any) error {
	return a.authorize(ctx, method)
}

func (a *MTLSAuthorizer) AuthorizeStream(ctx context.Context, method string) error {
	return a.authorize(ctx, method)
}

func (a *MTLSAuthorizer) authorize(ctx context.Context, method string) error {
	if len(a.methods) > 0 {
		if _, allowed := a.methods[method]; !allowed {
			return errors.New("gRPC method is not authorized for this boundary")
		}
	}
	remote, ok := peer.FromContext(ctx)
	if !ok || remote.AuthInfo == nil {
		return errors.New("verified mTLS peer is required")
	}
	tlsInfo, ok := remote.AuthInfo.(credentials.TLSInfo)
	if !ok || !verifiedClientCertificate(tlsInfo.State) {
		return errors.New("verified mTLS client certificate is required")
	}
	actual := sha256.Sum256(tlsInfo.State.PeerCertificates[0].Raw)
	for _, allowed := range a.fingerprints {
		if subtle.ConstantTimeCompare(actual[:], allowed[:]) == 1 {
			return nil
		}
	}
	return errors.New("mTLS client certificate is not authorized")
}

func verifiedClientCertificate(state tls.ConnectionState) bool {
	return len(state.PeerCertificates) > 0 && len(state.VerifiedChains) > 0
}
