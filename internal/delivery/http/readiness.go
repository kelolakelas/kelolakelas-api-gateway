package http

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const probeTimeout = time.Second

// ReadinessConfig supplies the mandatory service endpoints and optional cache probe.
//
// ChatURL is optional (KEL-122): empty disables the chat check entirely, so a
// deployment without chat-service stays healthy. When set it is probed at
// /health rather than /ready because chat-service only serves the liveness
// endpoint.
type ReadinessConfig struct {
	IdentityURL string
	AcademicURL string
	BillingURL  string
	ChatURL     string
	Redis       redis.Cmdable
}

func readinessHandler(cfg ReadinessConfig) gin.HandlerFunc {
	client := &http.Client{Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), probeTimeout)
		defer cancel()
		components := gin.H{}
		status, code := "healthy", http.StatusOK
		targets := []struct {
			name, raw, path string
		}{
			{"identity", cfg.IdentityURL, "/ready"},
			{"academic", cfg.AcademicURL, "/ready"},
			{"billing", cfg.BillingURL, "/ready"},
		}
		if strings.TrimSpace(cfg.ChatURL) != "" {
			targets = append(targets, struct {
				name, raw, path string
			}{"chat", cfg.ChatURL, "/health"})
		}
		for _, target := range targets {
			name, raw := target.name, target.raw
			parsed, err := url.Parse(raw)
			if err == nil && (parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "") {
				err = http.ErrNotSupported
			}
			if err == nil {
				parsed.Path = strings.TrimRight(parsed.Path, "/") + target.path
				parsed.RawQuery = ""
				var req *http.Request
				req, err = http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
				if err == nil {
					var resp *http.Response
					resp, err = client.Do(req)
					if err == nil {
						_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
						resp.Body.Close()
						if resp.StatusCode != http.StatusOK {
							err = http.ErrNotSupported
						}
					}
				}
			}
			if err != nil {
				components[name] = "unavailable"
				status, code = "unavailable", http.StatusServiceUnavailable
			} else {
				components[name] = "healthy"
			}
		}
		if cfg.Redis != nil && cfg.Redis.Ping(ctx).Err() == nil {
			components["redis"] = "healthy"
		} else {
			components["redis"] = "degraded"
			if code == http.StatusOK {
				status = "degraded"
			}
		}
		c.JSON(code, gin.H{"status": status, "service": "api-gateway", "components": components})
	}
}
