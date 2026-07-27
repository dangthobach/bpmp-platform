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
		return nil, errors.New("Redis rate limiter configuration is invalid")
	}
	return &Limiter{client: client, config: config}, nil
}

func (l *Limiter) Allow(ctx context.Context, subject string) (bool, error) {
	if subject == "" {
		return false, errors.New("rate limit subject is required")
	}
	bounded, cancel := context.WithTimeout(ctx, l.config.OperationTimeout)
	defer cancel()
	digest := sha256.Sum256([]byte(subject))
	key := l.config.Prefix + ":" + hex.EncodeToString(digest[:])
	result, err := fixedWindow.Run(
		bounded,
		l.client,
		[]string{key},
		l.config.Window.Milliseconds(),
		strconv.FormatUint(uint64(l.config.Requests), 10),
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
