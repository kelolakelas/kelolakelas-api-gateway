package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

func TestInvitationListAndRevokeAreProtectedIdentityRoutes(t *testing.T) {
	forwarded := 0
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded++
		if r.Header.Get("X-Tenant-ID") != "00000000-0000-0000-0000-000000000002" {
			t.Errorf("tenant header=%q", r.Header.Get("X-Tenant-ID"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer identity.Close()
	proxy, err := handler.NewProxyHandler(identity.URL, "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "invitation-test")
	gateway := httptest.NewServer(router)
	defer gateway.Close()
	claims := jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix(), "user_id": "00000000-0000-0000-0000-000000000001", "tenant_id": "00000000-0000-0000-0000-000000000002"}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("invitation-test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{{http.MethodGet, "/api/v1/invitations"}, {http.MethodDelete, "/api/v1/invitations/00000000-0000-0000-0000-000000000003"}} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Tenant-ID", "forged")
		anonymous := httptest.NewRecorder()
		router.ServeHTTP(anonymous, req)
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous=%d", tc.method, anonymous.Code)
		}
		req, err := http.NewRequest(tc.method, gateway.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+signed)
		req.Header.Set("X-Tenant-ID", "forged")
		authorized, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		authorized.Body.Close()
		if authorized.StatusCode != http.StatusNoContent {
			t.Fatalf("%s authorized=%d", tc.method, authorized.StatusCode)
		}
	}
	if forwarded != 2 {
		t.Fatalf("forwarded=%d", forwarded)
	}
}
