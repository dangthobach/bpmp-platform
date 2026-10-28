package ports

import (
	"context"
	"errors"
	"time"
)

var ErrBrowserSessionNotFound = errors.New("browser session not found")

type BrowserSession struct {
	TenantID           string
	ActorID            string
	SignedActorContext []byte
	CSRFTokenSHA256    []byte
	ExpiresAt          time.Time
}

type BrowserSessionStore interface {
	Get(context.Context, string) (BrowserSession, error)
}
