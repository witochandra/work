package work

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
		MaxActive:   10,
		MaxIdle:     10,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			return redis.Dial("tcp", redisHost)
		},
		Wait: true,
	}
}

func setupTestContext(t testing.TB) (string, *redis.Pool) {
	t.Helper()
	pool := newTestPool(t)
	ns := "work:" + t.Name()
	cleanKeyspace(ns, pool)
	t.Cleanup(func() {
		cleanKeyspace(ns, pool)
	})
	return ns, pool
}
