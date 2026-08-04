package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type fakeRedisClient struct {
	counts map[string]int64
	keys   []string
	err    error
}

func (client *fakeRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	client.keys = append(client.keys, keys[0])
	command := redis.NewCmd(ctx)
	if client.err != nil {
		command.SetErr(client.err)
		return command
	}
	if client.counts == nil {
		client.counts = make(map[string]int64)
	}
	client.counts[keys[0]]++
	windowSeconds := args[0].(int)
	command.SetVal(fmt.Sprintf("%d:%d", client.counts[keys[0]], windowSeconds))
	return command
}

func TestRateLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		path           string
		method         string
		remoteAddr     string
		requests       int
		redisErr       error
		wantStatuses   []int
		wantRemaining  string
		wantRetryAfter string
	}{
		{name: "below limit", path: "/api/v1/auth/login", method: http.MethodPost, remoteAddr: "10.0.0.1:1234", requests: 4, wantStatuses: []int{200, 200, 200, 200}, wantRemaining: "1"},
		{name: "exactly at limit", path: "/api/v1/auth/login", method: http.MethodPost, remoteAddr: "10.0.0.2:1234", requests: 5, wantStatuses: []int{200, 200, 200, 200, 200}, wantRemaining: "0"},
		{name: "over limit", path: "/api/v1/auth/login", method: http.MethodPost, remoteAddr: "10.0.0.3:1234", requests: 6, wantStatuses: []int{200, 200, 200, 200, 200, 429}, wantRemaining: "0", wantRetryAfter: "60"},
		{name: "Redis error fail open", path: "/api/v1/auth/login", method: http.MethodPost, remoteAddr: "10.0.0.4:1234", requests: 1, redisErr: errors.New("connection refused"), wantStatuses: []int{200}, wantRemaining: "5"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeRedisClient{err: test.redisErr}
			router := gin.New()
			router.Use(RateLimitMiddleware(client, RateLimitConfig{}, slog.Default()))
			router.Any("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })

			for index, wantStatus := range test.wantStatuses {
				req := httptest.NewRequest(test.method, test.path, nil)
				req.RemoteAddr = test.remoteAddr
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				if response.Code != wantStatus {
					t.Fatalf("request %d status=%d want=%d", index+1, response.Code, wantStatus)
				}
				if got := response.Header().Get("X-RateLimit-Limit"); got != "5" {
					t.Fatalf("request %d limit=%q", index+1, got)
				}
				if index == len(test.wantStatuses)-1 {
					if got := response.Header().Get("X-RateLimit-Remaining"); got != test.wantRemaining {
						t.Fatalf("request %d remaining=%q want=%q", index+1, got, test.wantRemaining)
					}
					if response.Code == http.StatusTooManyRequests {
						if got := response.Header().Get("Retry-After"); got != test.wantRetryAfter {
							t.Fatalf("retry-after=%q want=%q", got, test.wantRetryAfter)
						}
						if !strings.Contains(response.Body.String(), `"message":"Rate limit exceeded"`) {
							t.Fatalf("unexpected response body: %s", response.Body.String())
						}
					}
				}
			}
		})
	}
}

func TestRateLimitMiddlewareSeparatesCounters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeRedisClient{}
	router := gin.New()
	router.Use(RateLimitMiddleware(client, RateLimitConfig{Requests: 2, PublicRequests: 2, ProtectedRequests: 2}, slog.Default()))
	router.Any("/api/v1/one", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.Any("/api/v1/two", func(c *gin.Context) { c.Status(http.StatusOK) })

	requests := []struct {
		path, method, remoteAddr string
	}{
		{"/api/v1/one", http.MethodGet, "10.0.0.1:1234"},
		{"/api/v1/one", http.MethodGet, "10.0.0.2:1234"},
		{"/api/v1/one", http.MethodPost, "10.0.0.1:1234"},
		{"/api/v1/two", http.MethodGet, "10.0.0.1:1234"},
	}
	for _, request := range requests {
		req := httptest.NewRequest(request.method, request.path, nil)
		req.RemoteAddr = request.remoteAddr
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	if len(client.keys) != len(requests) {
		t.Fatalf("evaluated keys=%d want=%d", len(client.keys), len(requests))
	}
	seen := make(map[string]struct{})
	for _, key := range client.keys {
		seen[key] = struct{}{}
	}
	if len(seen) != len(requests) {
		t.Fatalf("distinct keys=%d want=%d", len(seen), len(requests))
	}
}
