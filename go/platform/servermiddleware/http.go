package servermiddleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type HTTPConfig struct {
	Service         string
	RequestTimeout  time.Duration
	MaxBodyBytes    int64
	TimeoutExempt   func(*http.Request) bool
	SecurityHeaders bool
	RateLimiter     *TokenBucket
}

func (c HTTPConfig) Validate() error {
	if c.Service == "" {
		return errors.New("HTTP middleware service name is required")
	}
	if c.RequestTimeout < 0 || c.MaxBodyBytes < 0 {
		return errors.New("HTTP middleware bounds cannot be negative")
	}
	return nil
}

func NewHTTP(config HTTPConfig, next http.Handler) (http.Handler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if next == nil {
		return nil, errors.New("HTTP middleware next handler is required")
	}
	wrapped := requestDeadline(config, next)
	wrapped = requestRateLimit(config, wrapped)
	wrapped = requestBounds(config, wrapped)
	wrapped = responseSecurity(config, wrapped)
	wrapped = recovery(wrapped)
	return requestmeta.HTTPMiddleware(config.Service, wrapped), nil
}

func requestRateLimit(config HTTPConfig, next http.Handler) http.Handler {
	if config.RateLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !config.RateLimiter.Allow() {
			requestmeta.WriteProblem(w, r, http.StatusTooManyRequests,
				"rate_limit_exceeded", "Rate limit exceeded", "", true)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				attrs := append(requestmeta.SlogAttrs(r.Context()),
					slog.String("panic_type", fmt.Sprintf("%T", recovered)))
				slog.ErrorContext(r.Context(), "HTTP handler panic recovered", attrs...)
				if state, ok := w.(interface{ ResponseCommitted() bool }); ok && state.ResponseCommitted() {
					panic(http.ErrAbortHandler)
				}
				requestmeta.WriteProblem(w, r, http.StatusInternalServerError,
					"internal_error", "Internal server error", "", false)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestDeadline(config HTTPConfig, next http.Handler) http.Handler {
	if config.RequestTimeout == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.TimeoutExempt != nil && config.TimeoutExempt(r) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), config.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestBounds(config HTTPConfig, next http.Handler) http.Handler {
	if config.MaxBodyBytes == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > config.MaxBodyBytes {
			requestmeta.WriteProblem(w, r, http.StatusRequestEntityTooLarge,
				"request_too_large", "Request body is too large", "", false)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, config.MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func responseSecurity(config HTTPConfig, next http.Handler) http.Handler {
	if !config.SecurityHeaders {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
