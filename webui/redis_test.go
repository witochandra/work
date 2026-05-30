package webui

import (
	"os"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
)

func newTestPool(t testing.TB) *redis.Pool {
	t.Helper()

	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "127.0.0.1:6379"
	}

	return &redis.Pool{
		MaxActive:   3,
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			return redis.Dial("tcp", redisHost)
		},
		Wait: true,
	}
}
