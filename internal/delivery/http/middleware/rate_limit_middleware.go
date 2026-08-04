package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const rateLimitScript = `
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return tostring(current) .. ':' .. tostring(redis.call('TTL', KEYS[1]))
`

type RedisClient interface {
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd
}

type RateLimitConfig struct {
	Requests                  int
	WindowSeconds             int
	PublicRequests            int
	ProtectedRequests         int
	SensitiveLoginRequests    int
	SensitiveRegisterRequests int
	WebhookRequests           int
	WebhookWindowSeconds      int
}

func (config RateLimitConfig) withDefaults() RateLimitConfig {
	if config.Requests <= 0 {
		config.Requests = 60
	}
	if config.WindowSeconds <= 0 {
		config.WindowSeconds = 60
	}
	if config.PublicRequests <= 0 {
		config.PublicRequests = config.Requests
	}
	if config.ProtectedRequests <= 0 {
		config.ProtectedRequests = 120
	}
	if config.SensitiveLoginRequests <= 0 {
		config.SensitiveLoginRequests = 5
	}
	if config.SensitiveRegisterRequests <= 0 {
		config.SensitiveRegisterRequests = 10
	}
	if config.WebhookRequests <= 0 {
		config.WebhookRequests = 120
	}
	if config.WebhookWindowSeconds <= 0 {
		config.WebhookWindowSeconds = config.WindowSeconds
	}
	return config
}

func RateLimitMiddleware(client RedisClient, config RateLimitConfig, logger *slog.Logger) gin.HandlerFunc {
	config = config.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/") {
			c.Next()
			return
		}

		limit, windowSeconds := config.limitFor(path)
		remaining := limit
		setRateLimitHeaders(c, limit, remaining)
		if client == nil {
			logger.Error("rate limiter Redis client is unavailable", "mode", "fail-open")
			c.Next()
			return
		}

		window := time.Now().Unix() / int64(windowSeconds)
		key := fmt.Sprintf("rate_limit:%s:%s:%s:%d", c.ClientIP(), c.Request.Method, path, window)
		result, err := client.Eval(c.Request.Context(), rateLimitScript, []string{key}, windowSeconds).Result()
		if err != nil {
			logger.Error("rate limiter Redis request failed", "error", err, "key", key, "mode", "fail-open")
			c.Next()
			return
		}

		count, ttl, err := parseRateLimitResult(result)
		if err != nil {
			logger.Error("rate limiter returned an invalid Redis result", "error", err, "key", key, "mode", "fail-open")
			c.Next()
			return
		}
		remaining = limit - int(count)
		if remaining < 0 {
			remaining = 0
		}
		setRateLimitHeaders(c, limit, remaining)
		if count > int64(limit) {
			retryAfter := ttl
			if retryAfter < 1 {
				retryAfter = int64(windowSeconds)
			}
			c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"status":  "error",
				"message": "Rate limit exceeded",
				"data":    nil,
			})
			return
		}

		c.Next()
	}
}

func (config RateLimitConfig) limitFor(path string) (int, int) {
	windowSeconds := config.WindowSeconds
	switch path {
	case "/api/v1/auth/login":
		return config.SensitiveLoginRequests, windowSeconds
	case "/api/v1/auth/register":
		return config.SensitiveRegisterRequests, windowSeconds
	case "/api/v1/billing/webhooks/duitku":
		return config.WebhookRequests, config.WebhookWindowSeconds
	case "/api/v1/tenants/register", "/api/v1/invitations/verify", "/api/v1/invitations/register":
		return config.PublicRequests, windowSeconds
	default:
		return config.ProtectedRequests, windowSeconds
	}
}

func setRateLimitHeaders(c *gin.Context, limit, remaining int) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
}

func parseRateLimitResult(result interface{}) (int64, int64, error) {
	value, ok := result.(string)
	if !ok {
		return 0, 0, fmt.Errorf("expected string, got %T", result)
	}
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected count:ttl, got %q", value)
	}
	count, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid count: %w", err)
	}
	ttl, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid ttl: %w", err)
	}
	return count, ttl, nil
}
