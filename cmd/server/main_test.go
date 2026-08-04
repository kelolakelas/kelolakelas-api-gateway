package main

import (
	"crypto/tls"
	"testing"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/config"
)

func TestBuildRedisOptions(t *testing.T) {
	options := buildRedisOptions(config.Config{
		RedisHost: "redis.example.com", RedisPort: "6380", RedisUsername: "upstash",
		RedisPassword: "secret", RedisTLS: true, RedisDB: 5,
	})
	if options.Username != "upstash" || options.Password != "secret" || options.DB != 5 {
		t.Fatalf("redis options=%+v", options)
	}
	if options.TLSConfig == nil || options.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("redis TLS config=%+v", options.TLSConfig)
	}
}
