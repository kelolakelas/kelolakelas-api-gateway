package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

func TestPlatformCreatorRequestQueueRoute(t *testing.T) {
	calls := 0
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/platform/creator-requests" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer identity.Close()
	proxy, err := handler.NewProxyHandler(identity.URL, "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(NewRouter(proxy, "queue-secret"))
	defer gateway.Close()
	for _, tc := range []struct {
		name   string
		claims jwt.MapClaims
		want   int
	}{
		{"anonymous", nil, 401},
		{"tenant", jwt.MapClaims{"tenant_id": "00000000-0000-0000-0000-000000000002"}, 403},
		{"parent", jwt.MapClaims{"is_parent": true}, 403},
		{"platform", jwt.MapClaims{"is_platform_admin": true, "platform_factor_version": 1}, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/v1/platform/creator-requests", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.claims != nil {
				tc.claims["exp"] = time.Now().Add(time.Hour).Unix()
				tc.claims["user_id"] = "00000000-0000-0000-0000-000000000001"
				signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tc.claims).SignedString([]byte("queue-secret"))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+signed)
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d", response.StatusCode, tc.want)
			}
		})
	}
	if calls != 1 {
		t.Fatalf("forwarded=%d want=1", calls)
	}
}
