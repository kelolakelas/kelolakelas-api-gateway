package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
)

func TestPasswordResetRoutesArePublicSensitiveProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Identity-Path", r.URL.Path)
		w.WriteHeader(204)
	}))
	defer identity.Close()
	proxy, err := handler.NewProxyHandler(identity.URL, "http://academic.invalid", "http://billing.invalid")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithRateLimit(proxy, "secret", nil, middleware.RateLimitConfig{SensitiveLoginRequests: 3, ProtectedRequests: 100}, slog.Default())
	gateway := httptest.NewServer(router)
	defer gateway.Close()
	for _, path := range []string{"/api/v1/auth/password-reset/request", "/api/v1/auth/password-reset/confirm"} {
		request, err := http.NewRequest(http.MethodPost, gateway.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		w := response
		if w.StatusCode != 204 || w.Header.Get("X-Identity-Path") != path || w.Header.Get("X-RateLimit-Limit") != "3" {
			t.Fatalf("%s: status=%d path=%q limit=%q", path, w.StatusCode, w.Header.Get("X-Identity-Path"), w.Header.Get("X-RateLimit-Limit"))
		}
	}
}
