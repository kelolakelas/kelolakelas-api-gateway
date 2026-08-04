package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/config"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Load environment variables
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		return
	}

	// Initialize proxy handler
	proxyHandler, err := handler.NewProxyHandler(cfg.IdentityServiceURL, cfg.AcademicServiceURL, cfg.BillingServiceURL)
	if err != nil {
		slog.Error("Failed to initialize proxy handler", "error", err)
		return
	}

	logger := slog.Default()
	redisClient := redis.NewClient(buildRedisOptions(cfg))
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Warn("Redis is unavailable; rate limiter is running in fail-open mode", "error", err)
	}

	// Setup Router
	r := http.NewRouterWithConfig(proxyHandler, cfg.JWTSecret, cfg.APPURL, redisClient, middleware.RateLimitConfig{
		Requests:                  cfg.RateLimitRequests,
		WindowSeconds:             cfg.RateLimitWindow,
		PublicRequests:            cfg.RateLimitPublic,
		ProtectedRequests:         cfg.RateLimitProtected,
		SensitiveLoginRequests:    cfg.RateLimitLogin,
		SensitiveRegisterRequests: cfg.RateLimitRegister,
		WebhookRequests:           cfg.RateLimitWebhook,
		WebhookWindowSeconds:      cfg.WebhookWindow,
	}, logger)

	logger.Info("Starting API Gateway", "port", cfg.Port)
	if err := r.Run("0.0.0.0:" + cfg.Port); err != nil {
		logger.Error("Failed to start API Gateway", "error", err)
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
