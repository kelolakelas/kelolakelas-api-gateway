package http

import (
	"context"
	"io"
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

func TestTenantSessionRoutesUseVerifiedContextAndSessionGate(t *testing.T) {
	const tenant = "00000000-0000-0000-0000-000000000002"
	calls := 0
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Tenant-ID") != tenant || r.Header.Get("X-Internal-Service-Credential") != "" {
			t.Error("client context survived gateway")
		}
		if r.URL.Path == "/api/v1/auth/tenant" {
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"tenant_id":"target"}` {
				t.Error("switch body changed")
			}
		}
		w.WriteHeader(204)
	}))
	defer identity.Close()
	proxy, _ := handler.NewProxyHandler(identity.URL, "http://academic.invalid", "http://billing.invalid")
	status := 204
	check := func(context.Context, string) (int, error) { return status, nil }
	router, err := NewRouterWithClientIPTrust(proxy, "secret", "", nil, middleware.RateLimitConfig{}, slog.Default(), ClientIPTrust{}, check)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(router)
	defer gateway.Close()
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "caller", "tenant_id": tenant, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("secret"))
	for _, path := range []string{"/api/v1/auth/memberships", "/api/v1/auth/tenant"} {
		method, body := "GET", ""
		if strings.HasSuffix(path, "tenant") {
			method, body = "POST", `{"tenant_id":"target"}`
		}
		for _, tc := range []struct {
			token   string
			session int
			want    int
		}{{"", 204, 401}, {token, 401, 401}, {token, 503, 503}, {token, 204, 204}} {
			status = tc.session
			req, err := http.NewRequest(method, gateway.URL+path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Tenant-ID", "forged")
			req.Header.Set("X-Internal-Service-Credential", "forged")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, w.Body)
			w.Body.Close()
			if w.StatusCode != tc.want {
				t.Fatalf("%s got %d want %d", path, w.StatusCode, tc.want)
			}
		}
	}
	if calls != 2 {
		t.Fatalf("upstream called %d times", calls)
	}
}
