package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newAccessLogRouter(t *testing.T, handler gin.HandlerFunc) (*gin.Engine, *bytes.Buffer) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))

	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(AccessLogMiddleware(logger))
	router.Any("/api/v1/students", handler)
	return router, &buffer
}

func accessLogLines(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if raw == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v (line=%s)", err, raw)
		}
		lines = append(lines, entry)
	}
	return lines
}

// TestRequestIDMiddlewareGeneratesIdentifier proves every response carries a
// request ID even when the client sends none.
func TestRequestIDMiddlewareGeneratesIdentifier(t *testing.T) {
	router, _ := newAccessLogRouter(t, func(c *gin.Context) { c.Status(http.StatusOK) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/students", nil))

	got := response.Header().Get(RequestIDHeader)
	if got == "" {
		t.Fatalf("response is missing %s", RequestIDHeader)
	}
	if len(got) != 32 {
		t.Fatalf("generated request id length=%d value=%q, want 32 hex characters", len(got), got)
	}
}

// TestRequestIDMiddlewareRejectsUnsafeInboundIdentifier proves a malformed or
// oversized inbound identifier is replaced instead of trusted.
func TestRequestIDMiddlewareRejectsUnsafeInboundIdentifier(t *testing.T) {
	tests := []struct {
		name string
		sent string
	}{
		{name: "injected newline", sent: "abc\ndef"},
		{name: "space separated", sent: "abc def"},
		{name: "too long", sent: strings.Repeat("a", 65)},
		{name: "control character", sent: "abc\x00"},
		{name: "empty after trim", sent: "   "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, _ := newAccessLogRouter(t, func(c *gin.Context) { c.Status(http.StatusOK) })

			request := httptest.NewRequest(http.MethodGet, "/api/v1/students", nil)
			request.Header.Set(RequestIDHeader, test.sent)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			got := response.Header().Get(RequestIDHeader)
			if got == "" || got == test.sent {
				t.Fatalf("unsafe identifier %q was echoed back as %q", test.sent, got)
			}
		})
	}
}

// TestRequestIDMiddlewarePreservesValidInboundIdentifier proves a well formed
// client identifier is reused rather than replaced.
func TestRequestIDMiddlewarePreservesValidInboundIdentifier(t *testing.T) {
	for _, sent := range []string{"3f6f0a1c-9d2e-4a11-8abc-0123456789ab", "checkout.run-42_v2", strings.Repeat("a", 64)} {
		t.Run(sent, func(t *testing.T) {
			router, _ := newAccessLogRouter(t, func(c *gin.Context) { c.Status(http.StatusOK) })

			request := httptest.NewRequest(http.MethodGet, "/api/v1/students", nil)
			request.Header.Set(RequestIDHeader, sent)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if got := response.Header().Get(RequestIDHeader); got != sent {
				t.Fatalf("request id=%q want=%q", got, sent)
			}
		})
	}
}

// TestAccessLogMiddlewareWritesOneStructuredLinePerRequest covers the required
// fields and proves the log line carries neither the Authorization header nor
// the query string.
func TestAccessLogMiddlewareWritesOneStructuredLinePerRequest(t *testing.T) {
	router, buffer := newAccessLogRouter(t, func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/api/v1/students?invitation_token=super-secret-token", nil)
	request.Header.Set("Authorization", "Bearer secret-token-value")
	request.Header.Set(RequestIDHeader, "trace-abc")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	lines := accessLogLines(t, buffer)
	if len(lines) != 1 {
		t.Fatalf("access log lines=%d want=1 (log=%s)", len(lines), buffer.String())
	}
	entry := lines[0]

	if entry["msg"] != "gateway access" {
		t.Fatalf("msg=%v", entry["msg"])
	}
	if entry["request_id"] != "trace-abc" {
		t.Fatalf("request_id=%v", entry["request_id"])
	}
	if entry["method"] != http.MethodGet {
		t.Fatalf("method=%v", entry["method"])
	}
	if entry["path"] != "/api/v1/students" {
		t.Fatalf("path=%v", entry["path"])
	}
	if status, ok := entry["status"].(float64); !ok || int(status) != http.StatusNoContent {
		t.Fatalf("status=%v", entry["status"])
	}
	if _, ok := entry["latency_ms"].(float64); !ok {
		t.Fatalf("latency_ms missing or not numeric: %v", entry["latency_ms"])
	}
	if _, ok := entry["client_ip"].(string); !ok {
		t.Fatalf("client_ip missing: %v", entry["client_ip"])
	}

	raw := buffer.String()
	for _, forbidden := range []string{"secret-token-value", "invitation_token", "super-secret-token"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("access log leaked %q: %s", forbidden, raw)
		}
	}
}

// TestAccessLogMiddlewareRecordsRejectedRequests proves requests aborted before
// a proxy handler still produce exactly one access log line with their status.
func TestAccessLogMiddlewareRecordsRejectedRequests(t *testing.T) {
	router, buffer := newAccessLogRouter(t, func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error"})
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/students", nil))

	lines := accessLogLines(t, buffer)
	if len(lines) != 1 {
		t.Fatalf("access log lines=%d want=1", len(lines))
	}
	if status, _ := lines[0]["status"].(float64); int(status) != http.StatusUnauthorized {
		t.Fatalf("status=%v want=%d", lines[0]["status"], http.StatusUnauthorized)
	}
}
