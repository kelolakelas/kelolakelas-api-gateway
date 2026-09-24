package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/config"
	gatewayhttp "github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Initialize JSON logging so the structured gateway access log is emitted
	// as JSON, consistent with the downstream services.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Load environment variables
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize proxy handler. Timeouts and the body limit come from
	// configuration so an operator can raise them for a slow downstream or a
	// larger payload without a rebuild.
	proxyHandler, err := handler.NewProxyHandlerWithOptions(
		cfg.IdentityServiceURL, cfg.AcademicServiceURL, cfg.BillingServiceURL,
		handler.ProxyOptions{
			UpstreamTimeout: time.Duration(cfg.ProxyUpstreamTimeout) * time.Second,
			MaxBodyBytes:    cfg.ProxyMaxBodyBytes,
			Logger:          logger,
		},
	)
	if err != nil {
		slog.Error("Failed to initialize proxy handler", "error", err)
		return
	}

	redisClient := redis.NewClient(buildRedisOptions(cfg))
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Warn("Redis is unavailable; rate limiter is running in fail-open mode", "error", err)
	}

	// Setup Router
	r, err := gatewayhttp.NewRouterWithClientIPTrust(proxyHandler, cfg.JWTSecret, cfg.APPURL, redisClient, middleware.RateLimitConfig{
		Requests:                  cfg.RateLimitRequests,
		WindowSeconds:             cfg.RateLimitWindow,
		PublicRequests:            cfg.RateLimitPublic,
		ProtectedRequests:         cfg.RateLimitProtected,
		SensitiveLoginRequests:    cfg.RateLimitLogin,
		SensitiveRegisterRequests: cfg.RateLimitRegister,
		WebhookRequests:           cfg.RateLimitWebhook,
		WebhookWindowSeconds:      cfg.WebhookWindow,
	}, logger, gatewayhttp.ClientIPTrust{
		TrustedProxies: cfg.TrustedProxies,
		Header:         cfg.TrustedClientIPHeader,
	})
	if err != nil {
		slog.Error("Failed to configure client IP trust", "error", err)
		os.Exit(1)
	}

	// http.Server is configured explicitly rather than through gin's Run helper,
	// which leaves every timeout unbounded: a client that opens a connection and
	// stops sending would otherwise hold a goroutine and a file descriptor
	// forever. The write timeout is validated at configuration load to exceed the
	// upstream timeout, so a slow but healthy downstream is never cut short by
	// the server itself.
	server := newHTTPServer(cfg, r)

	logger.Info("Starting API Gateway",
		"port", cfg.Port,
		"proxy_upstream_timeout_seconds", cfg.ProxyUpstreamTimeout,
		"proxy_max_body_bytes", cfg.ProxyMaxBodyBytes,
		"server_write_timeout_seconds", cfg.ServerWriteTimeout,
		"trusted_proxy_count", len(cfg.TrustedProxies),
		"trusted_client_ip_header", cfg.TrustedClientIPHeader,
	)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("Failed to start API Gateway", "error", err)
	}
}

// newHTTPServer applies the configured timeouts to the gateway's HTTP server.
func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              "0.0.0.0:" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: time.Duration(cfg.ServerReadHeaderTimeout) * time.Second,
		ReadTimeout:       time.Duration(cfg.ServerReadTimeout) * time.Second,
		WriteTimeout:      time.Duration(cfg.ServerWriteTimeout) * time.Second,
		IdleTimeout:       time.Duration(cfg.ServerIdleTimeout) * time.Second,
	}
}

func buildRedisOptions(cfg config.Config) *redis.Options {
	return &redis.Options{
		Addr:      net.JoinHostPort(cfg.RedisHost, cfg.RedisPort),
		Username:  cfg.RedisUsername,
		Password:  cfg.RedisPassword,
		DB:        cfg.RedisDB,
		TLSConfig: redisTLSConfig(cfg.RedisTLS),
	}
}

func redisTLSConfig(enabled bool) *tls.Config {
	if !enabled {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12}
}
