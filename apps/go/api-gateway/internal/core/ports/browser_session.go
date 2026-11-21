package ports

import (
	"context"
	"errors"
	"time"
)

var ErrBrowserSessionNotFound = errors.New("browser session not found")
var ErrOIDCTransactionNotFound = errors.New("OIDC login transaction not found")

type BrowserSession struct {
	TenantID           string
	ActorID            string
	SignedActorContext []byte
	CSRFTokenSHA256    []byte
	ExpiresAt          time.Time
}

type BrowserSessionStore interface {
	Get(context.Context, string) (BrowserSession, error)
	Create(context.Context, string, BrowserSession) error
	Delete(context.Context, string) error
}

type OIDCLoginTransaction struct {
	CodeVerifier string
	Nonce        string
	TenantID     string
	ExpiresAt    time.Time
}

type OIDCTransactionStore interface {
	Save(context.Context, string, OIDCLoginTransaction) error
	Consume(context.Context, string) (OIDCLoginTransaction, error)
}
