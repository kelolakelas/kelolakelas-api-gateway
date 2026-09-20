package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// ProxyTargetContextKey is set by proxy handlers to record which downstream
// service served the request, so the single gateway access log line can name
// the target without emitting a second log line per request.
const ProxyTargetContextKey = "proxy_target"

// AccessLogMiddleware writes exactly one structured log line per request
// containing the request ID, method, path, response status, latency and client
// IP. It deliberately omits the Authorization header, the request body and the
// query string, which may carry invitation tokens.
//
// It replaces the per-service proxy logs that previously recorded only method
// and path without status, latency or a correlation identifier.
//
// Register it after RequestIDMiddleware so the resolved identifier is available,
// and before CORS and rate limiting so rejected requests are still logged once.
func AccessLogMiddleware(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}

	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		// URL.Path excludes the raw query string by construction, so tokens
		// passed as query parameters are never written to the log.
		attributes := []any{
			"request_id", RequestIDFromContext(c),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		}
		if target := c.GetString(ProxyTargetContextKey); target != "" {
			attributes = append(attributes, "target", target)
		}

		logger.Info("gateway access", attributes...)
	}
}
