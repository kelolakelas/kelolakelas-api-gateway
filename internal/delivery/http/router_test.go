package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
)

// closeNotifyRecorder provides the deprecated capability ReverseProxy still
// requests from the underlying Gin response writer during direct router tests.
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func newCloseNotifyRecorder() *closeNotifyRecorder {
	return &closeNotifyRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func TestProtectedRoutesProxyToExpectedService(t *testing.T) {
	services := map[string]*httptest.Server{}
	for _, name := range []string{"identity", "academic", "billing"} {
		serviceName := name
		services[name] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-Service", serviceName)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer services[name].Close()
	}

	proxy, err := handler.NewProxyHandler(services["identity"].URL, services["academic"].URL, services["billing"].URL)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "test-secret")
	gateway := httptest.NewServer(router)
	defer gateway.Close()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   "00000000-0000-0000-0000-000000000001",
		"tenant_id": "00000000-0000-0000-0000-000000000002",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct{ name, path, service string }{
		{name: "student", path: "/api/v1/students", service: "academic"},
		{name: "enrollment", path: "/api/v1/enrollments", service: "academic"},
		{name: "session", path: "/api/v1/sessions", service: "academic"},
		{name: "session attendees", path: "/api/v1/sessions/00000000-0000-0000-0000-000000000003/attendees", service: "academic"},
		{name: "tutor", path: "/api/v1/tutors", service: "identity"},
		{name: "attendance", path: "/api/v1/attendance", service: "academic"},
		{name: "report", path: "/api/v1/reports", service: "academic"},
		{name: "billing", path: "/api/v1/billing/transactions", service: "billing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, gateway.URL+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+tokenString)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("status=%d", response.StatusCode)
			}
			if got := response.Header.Get("X-Test-Service"); got != test.service {
				t.Fatalf("service=%q want=%q", got, test.service)
			}
		})
	}
}

// TestClassUpdateRouteIsProtectedAcademicProxy proves the tenant-facing class
// update endpoint is exposed through the gateway and forwarded to the academic
// service with the client path and method preserved. The gateway must not
// bypass authentication: an unauthenticated request is rejected before proxying.
func TestClassUpdateRouteIsProtectedAcademicProxy(t *testing.T) {
	var gotPath, gotMethod string
	academic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer academic.Close()

	proxy, err := handler.NewProxyHandler("http://identity", academic.URL, "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "secret")

	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPatch, "/api/v1/classes/00000000-0000-0000-0000-000000000001", strings.NewReader(`{"name":"X"}`)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d want=%d", unauthenticated.Code, http.StatusUnauthorized)
	}
	if gotPath != "" {
		t.Fatalf("unauthenticated request must not reach the academic service, got path %s", gotPath)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   "00000000-0000-0000-0000-000000000001",
		"tenant_id": "00000000-0000-0000-0000-000000000002",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	authorized := newCloseNotifyRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/classes/00000000-0000-0000-0000-000000000001", strings.NewReader(`{"name":"X"}`))
	request.Header.Set("Authorization", "Bearer "+tokenString)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(authorized, request)

	if authorized.Code != http.StatusNoContent {
		t.Fatalf("authorized status=%d body=%s", authorized.Code, authorized.Body.String())
	}
	if gotPath != "/api/v1/classes/00000000-0000-0000-0000-000000000001" || gotMethod != http.MethodPatch {
		t.Fatalf("proxied method=%s path=%s", gotMethod, gotPath)
	}
}

// TestClassPublicationRouteStillWorks guards against the new PATCH /classes/:id
// route shadowing the pre-existing publication toggle.
func TestClassPublicationRouteStillWorks(t *testing.T) {
	var gotPath string
	academic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer academic.Close()

	proxy, err := handler.NewProxyHandler("http://identity", academic.URL, "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   "00000000-0000-0000-0000-000000000001",
		"tenant_id": "00000000-0000-0000-0000-000000000002",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/classes/00000000-0000-0000-0000-000000000001/published", strings.NewReader(`{"is_published":true}`))
	request.Header.Set("Authorization", "Bearer "+tokenString)
	recorder := newCloseNotifyRecorder()
	NewRouter(proxy, "secret").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || gotPath != "/api/v1/classes/00000000-0000-0000-0000-000000000001/published" {
		t.Fatalf("status=%d path=%s", recorder.Code, gotPath)
	}
}

func TestHealthEndpoint(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	NewRouter(proxy, "secret").ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content-type=%q", got)
	}
	if !strings.Contains(recorder.Body.String(), `"service":"api-gateway"`) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestCatalogPublicAndEnrollmentProtected(t *testing.T) {
	var gotPath string
	academic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer academic.Close()
	proxy, err := handler.NewProxyHandler("http://identity", academic.URL, "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "secret")
	public := newCloseNotifyRecorder()
	router.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/classes", nil))
	if public.Code != http.StatusNoContent || gotPath != "/api/v1/catalog/classes" {
		t.Fatalf("public status=%d path=%s", public.Code, gotPath)
	}
	protected := httptest.NewRecorder()
	router.ServeHTTP(protected, httptest.NewRequest(http.MethodPost, "/api/v1/catalog/classes/00000000-0000-0000-0000-000000000001/enrollments", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("enrollment status=%d", protected.Code)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "00000000-0000-0000-0000-000000000001", "is_parent": true, "exp": time.Now().Add(time.Hour).Unix()})
	tokenString, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	authorized := newCloseNotifyRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/classes/00000000-0000-0000-0000-000000000001/enrollments", nil)
	request.Header.Set("Authorization", "Bearer "+tokenString)
	router.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusNoContent || gotPath != "/api/v1/catalog/classes/00000000-0000-0000-0000-000000000001/enrollments" {
		t.Fatalf("authorized status=%d path=%s", authorized.Code, gotPath)
	}
	statusMutation := httptest.NewRecorder()
	router.ServeHTTP(statusMutation, httptest.NewRequest(http.MethodPut, "/api/v1/enrollments/00000000-0000-0000-0000-000000000001/status", nil))
	if statusMutation.Code != http.StatusNotFound {
		t.Fatalf("status mutation route=%d, want %d", statusMutation.Code, http.StatusNotFound)
	}
}

func TestUserFacingBillingTransactionCreationIsNotRouted(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": "00000000-0000-0000-0000-000000000001",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/billing/transactions", nil)
	request.Header.Set("Authorization", "Bearer "+tokenString)
	recorder := httptest.NewRecorder()
	NewRouter(proxy, "secret").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestRequestIDIsForwardedToDownstreamService proves the identifier returned to
// the client is exactly the identifier the downstream service receives, for
// both a client supplied and a gateway generated request ID.
func TestRequestIDIsForwardedToDownstreamService(t *testing.T) {
	tests := []struct {
		name             string
		inboundValue     string
		wantDownstreamIs func(t *testing.T, responseID, downstreamID string)
	}{
		{
			name:         "client supplied identifier is reused",
			inboundValue: "trace-9f8e7d6c",
			wantDownstreamIs: func(t *testing.T, responseID, downstreamID string) {
				t.Helper()
				if responseID != "trace-9f8e7d6c" {
					t.Fatalf("response request id=%q want=%q", responseID, "trace-9f8e7d6c")
				}
				if downstreamID != "trace-9f8e7d6c" {
					t.Fatalf("downstream request id=%q want=%q", downstreamID, "trace-9f8e7d6c")
				}
			},
		},
		{
			name:         "unsafe identifier is replaced and the replacement is forwarded",
			inboundValue: "bad value with spaces",
			wantDownstreamIs: func(t *testing.T, responseID, downstreamID string) {
				t.Helper()
				if responseID == "" || responseID == "bad value with spaces" {
					t.Fatalf("response request id=%q, want a generated identifier", responseID)
				}
				if downstreamID != responseID {
					t.Fatalf("downstream request id=%q want=%q", downstreamID, responseID)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var downstreamID string
			academic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downstreamID = r.Header.Get("X-Request-ID")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer academic.Close()

			proxy, err := handler.NewProxyHandler("http://identity", academic.URL, "http://billing")
			if err != nil {
				t.Fatal(err)
			}
			token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"user_id":   "00000000-0000-0000-0000-000000000001",
				"tenant_id": "00000000-0000-0000-0000-000000000002",
				"exp":       time.Now().Add(time.Hour).Unix(),
			})
			tokenString, err := token.SignedString([]byte("secret"))
			if err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequest(http.MethodGet, "/api/v1/students", nil)
			request.Header.Set("Authorization", "Bearer "+tokenString)
			request.Header.Set("X-Request-ID", test.inboundValue)
			recorder := newCloseNotifyRecorder()
			NewRouter(proxy, "secret").ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status=%d", recorder.Code)
			}
			test.wantDownstreamIs(t, recorder.Header().Get("X-Request-ID"), downstreamID)
		})
	}
}

// TestRequestIDReturnedOnUnroutedRequests proves the identifier is still
// returned when routing fails, so a client can correlate a 404.
func TestRequestIDReturnedOnUnroutedRequests(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	NewRouter(proxy, "secret").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d", recorder.Code, http.StatusNotFound)
	}
	if got := recorder.Header().Get("X-Request-ID"); got == "" {
		t.Fatal("response is missing X-Request-ID on a not found response")
	}
}

// TestProxiedRequestEmitsExactlyOneAccessLogLine proves the per-service proxy
// logs were replaced, not duplicated: a proxied request produces a single
// structured access log line carrying the documented fields.
func TestProxiedRequestEmitsExactlyOneAccessLogLine(t *testing.T) {
	academic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer academic.Close()

	proxy, err := handler.NewProxyHandler("http://identity", academic.URL, "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   "00000000-0000-0000-0000-000000000001",
		"tenant_id": "00000000-0000-0000-0000-000000000002",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))
	router := NewRouterWithConfig(proxy, "secret", "", nil, middleware.RateLimitConfig{}, logger)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/students", nil)
	request.Header.Set("Authorization", "Bearer "+tokenString)
	request.Header.Set("X-Request-ID", "one-line-trace")
	recorder := newCloseNotifyRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d", recorder.Code)
	}

	// The rate limiter logs its own fail-open warning in this test because no
	// Redis client is injected; only access log lines are counted here.
	var accessLogs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(line, "Proxying request") {
			t.Fatalf("per-service proxy log was not replaced: %s", line)
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if entry["msg"] == "gateway access" {
			accessLogs = append(accessLogs, entry)
		}
	}
	if len(accessLogs) != 1 {
		t.Fatalf("access log lines=%d want=1:\n%s", len(accessLogs), buffer.String())
	}
	entry := accessLogs[0]
	for field, want := range map[string]any{
		"request_id": "one-line-trace",
		"method":     http.MethodGet,
		"path":       "/api/v1/students",
		"status":     float64(http.StatusNoContent),
		"target":     "academic-service",
	} {
		if entry[field] != want {
			t.Fatalf("access log field %s=%v want=%v", field, entry[field], want)
		}
	}
	if _, ok := entry["latency_ms"].(float64); !ok {
		t.Fatalf("latency_ms=%v", entry["latency_ms"])
	}
	if _, ok := entry["client_ip"].(string); !ok {
		t.Fatalf("client_ip=%v", entry["client_ip"])
	}
}

// TestRequestIDOnNonProxiedRoutes covers the health and Swagger routes, which
// never reach a proxy handler but must still carry a correlation identifier.
func TestRequestIDOnNonProxiedRoutes(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "secret")

	for _, path := range []string{"/health", "/swagger", "/swagger/index.html"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if got := recorder.Header().Get("X-Request-ID"); got == "" {
				t.Fatalf("%s response is missing X-Request-ID", path)
			}
		})
	}
}

// TestRequestIDSurvivesCORSRejection proves a request rejected before proxying
// still returns and logs an identifier.
func TestRequestIDSurvivesCORSRejection(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))
	router := NewRouterWithConfig(proxy, "secret", "https://app.example.com", nil, middleware.RateLimitConfig{}, logger)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/classes", nil)
	request.Header.Set("Origin", "https://evil.example.com")
	request.Header.Set("X-Request-ID", "cors-trace")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d", recorder.Code, http.StatusForbidden)
	}
	if got := recorder.Header().Get("X-Request-ID"); got != "cors-trace" {
		t.Fatalf("request id=%q want=cors-trace", got)
	}
	if !strings.Contains(buffer.String(), `"status":403`) || !strings.Contains(buffer.String(), `"request_id":"cors-trace"`) {
		t.Fatalf("rejected request was not logged with its status and identifier: %s", buffer.String())
	}
}

// TestRequestIDSurvivesRateLimitRejection proves a rate limited request returns
// and logs the same identifier.
func TestRequestIDSurvivesRateLimitRejection(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))
	router := NewRouterWithConfig(proxy, "secret", "", nil,
		middleware.RateLimitConfig{Requests: 1, WindowSeconds: 60, ProtectedRequests: 1}, logger)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.Header.Set("X-Request-ID", "limit-trace")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	// No Redis client is injected, so the limiter fails open. The identifier
	// must still be present and logged exactly once.
	if got := recorder.Header().Get("X-Request-ID"); got != "limit-trace" {
		t.Fatalf("request id=%q want=limit-trace", got)
	}
	if strings.Count(buffer.String(), `"msg":"gateway access"`) != 1 {
		t.Fatalf("want exactly one access log line: %s", buffer.String())
	}
}
