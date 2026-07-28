package redislimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var fixedWindow = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
if current <= tonumber(ARGV[2]) then
  return 1
end
return 0
`)

type Config struct {
	Prefix           string
	Requests         uint32
	Window           time.Duration
	OperationTimeout time.Duration
}

type Limiter struct {
	client *redis.Client
	config Config
}

func New(client *redis.Client, config Config) (*Limiter, error) {
	if client == nil ||
		config.Prefix == "" ||
		config.Requests == 0 ||
		config.Window <= 0 ||
		config.OperationTimeout <= 0 {
		return nil, errors.New("redis rate limiter configuration is invalid")
	}
	return &Limiter{client: client, config: config}, nil
}

func NewDynamic(
	client *redis.Client,
	prefix string,
	operationTimeout time.Duration,
) (*Limiter, error) {
	if client == nil || prefix == "" || operationTimeout <= 0 {
		return nil, errors.New("dynamic Redis rate limiter configuration is invalid")
	}
	return &Limiter{
		client: client,
		config: Config{
			Prefix:           prefix,
			OperationTimeout: operationTimeout,
		},
	}, nil
}

func (l *Limiter) Allow(ctx context.Context, subject string) (bool, error) {
	if l.config.Requests == 0 || l.config.Window <= 0 {
		return false, errors.New("static rate limit policy is not configured")
	}
	return l.AllowPolicy(ctx, subject, l.config.Requests, l.config.Window)
}

func (l *Limiter) AllowPolicy(
	ctx context.Context,
	subject string,
	requests uint32,
	window time.Duration,
) (bool, error) {
	if subject == "" {
		return false, errors.New("rate limit subject is required")
	}
	if requests == 0 || window <= 0 {
		return false, errors.New("rate limit policy is invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, l.config.OperationTimeout)
	defer cancel()
	digest := sha256.Sum256([]byte(subject))
	key := l.config.Prefix + ":" + hex.EncodeToString(digest[:])
	result, err := fixedWindow.Run(
		bounded,
		l.client,
		[]string{key},
		window.Milliseconds(),
		strconv.FormatUint(uint64(requests), 10),
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
