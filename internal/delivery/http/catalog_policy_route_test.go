package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

func TestCatalogPolicyGatewayRequiresPlatform(t *testing.T) {
	calls := 0
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
	defer identity.Close()
	proxy, err := handler.NewProxyHandler(identity.URL, "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(NewRouter(proxy, "catalog-secret"))
	defer gateway.Close()
	token := func(claims jwt.MapClaims) string {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
		claims["user_id"] = "00000000-0000-0000-0000-000000000001"
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("catalog-secret"))
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/platform/catalog-policy"}, {"POST", "/api/v1/platform/catalog-policy/close"}, {"POST", "/api/v1/platform/catalog-policy/open"}} {
		for _, tc := range []struct {
			name, bearer string
			want         int
		}{{"anonymous", "", 401}, {"tenant", token(jwt.MapClaims{"tenant_id": "00000000-0000-0000-0000-000000000002"}), 403}, {"parent", token(jwt.MapClaims{"is_parent": true}), 403}, {"platform", token(jwt.MapClaims{"is_platform_admin": true, "platform_factor_version": 1}), 204}} {
			t.Run(route.path+"/"+tc.name, func(t *testing.T) {
				req, err := http.NewRequest(route.method, gateway.URL+route.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if tc.bearer != "" {
					req.Header.Set("Authorization", "Bearer "+tc.bearer)
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
	}
	if calls != 3 {
		t.Fatalf("forwarded=%d want=3", calls)
	}
}
