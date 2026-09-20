package main

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"

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

// TestNewHTTPServerAppliesConfiguredTimeouts proves the server the gateway runs
// bounds every phase of a connection. Without this the default gin Run helper
// leaves them at zero, which means no limit at all.
func TestNewHTTPServerAppliesConfiguredTimeouts(t *testing.T) {
	server := newHTTPServer(config.Config{
		Port:                    "8123",
		ServerReadHeaderTimeout: 5,
		ServerReadTimeout:       30,
		ServerWriteTimeout:      60,
		ServerIdleTimeout:       120,
	}, http.NewServeMux())

	if server.Addr != "0.0.0.0:8123" {
		t.Fatalf("addr=%q", server.Addr)
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout=%s want=5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout=%s want=30s", server.ReadTimeout)
	}
	if server.WriteTimeout != 60*time.Second {
		t.Fatalf("WriteTimeout=%s want=60s", server.WriteTimeout)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout=%s want=120s", server.IdleTimeout)
	}
	// A write timeout that does not clear the upstream timeout would cut a
	// response the proxy is still waiting for.
	if server.WriteTimeout <= 30*time.Second {
		t.Fatalf("WriteTimeout=%s does not exceed the default upstream timeout", server.WriteTimeout)
	}
}
