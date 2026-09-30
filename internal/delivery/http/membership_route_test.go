package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

// TestMembershipRouteRequiresTenantToken pins the KEL-136 route table: the
// membership read is a protected tenant route, so an anonymous caller gets 401
// and a tenantless platform token gets 403 at the gateway, a tenant token is
// forwarded to identity, and a parent token keeps the parent-bypass behaviour
// the other tenant routes already have (identity refuses it with 403 because
// a parent has no tenant claim to answer for).
func TestMembershipRouteRequiresTenantToken(t *testing.T) {
	calls := 0
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer identity.Close()
	proxy, err := handler.NewProxyHandler(identity.URL, "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "membership-test")
	gateway := httptest.NewServer(router)
	defer gateway.Close()
	token := func(claims jwt.MapClaims) string {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
		claims["user_id"] = "00000000-0000-0000-0000-000000000001"
		claims["role_id"] = "00000000-0000-0000-0000-000000000003"
		claims["member_id"] = "00000000-0000-0000-0000-000000000004"
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("membership-test"))
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	tenant := token(jwt.MapClaims{"tenant_id": "00000000-0000-0000-0000-000000000002"})
	parent := token(jwt.MapClaims{"is_parent": true, "tenant_id": "00000000-0000-0000-0000-000000000000"})
	platform := token(jwt.MapClaims{"is_platform_admin": true, "platform_factor_version": 1})
	for _, tc := range []struct {
		name, value string
		want        int
	}{{"anonymous", "", 401}, {"platform", platform, 403}, {"tenant", tenant, 204}, {"parent forwarded for identity denial", parent, 204}} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/v1/members/me/membership", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.value != "" {
				req.Header.Set("Authorization", "Bearer "+tc.value)
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
	if calls != 2 {
		t.Fatalf("identity calls=%d; anonymous and platform requests should not be forwarded", calls)
	}
}
