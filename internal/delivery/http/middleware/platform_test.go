package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestPlatformPrincipalBoundary(t *testing.T) {
	cases := []struct {
		name             string
		claims           jwt.MapClaims
		platform, tenant int
	}{
		{"platform only", jwt.MapClaims{"user_id": "admin", "is_platform_admin": true, "platform_factor_version": 1}, 204, 403},
		{"dual membership", jwt.MapClaims{"user_id": "admin", "is_platform_admin": true, "platform_factor_version": 1, "tenant_id": "11111111-1111-1111-1111-111111111111"}, 204, 204},
		{"tenant creator", jwt.MapClaims{"user_id": "creator", "tenant_id": "11111111-1111-1111-1111-111111111111"}, 403, 204},
		{"custom tenant role", jwt.MapClaims{"user_id": "member", "tenant_id": "11111111-1111-1111-1111-111111111111", "role_id": "22222222-2222-2222-2222-222222222222"}, 403, 204},
		{"parent", jwt.MapClaims{"user_id": "parent", "is_parent": true}, 403, 204},
		{"old tenantless token", jwt.MapClaims{"user_id": "old"}, 401, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(AuthMiddleware(authTestSecret))
			r.GET("/platform", RequirePlatform(), func(c *gin.Context) { c.Status(204) })
			r.GET("/tenant", RequireTenant(), func(c *gin.Context) { c.Status(204) })
			for _, path := range []string{"/platform", "/tenant"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Authorization", "Bearer "+authTestToken(t, tc.claims))
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				want := tc.platform
				if path == "/tenant" {
					want = tc.tenant
				}
				if w.Code != want {
					t.Fatalf("%s status=%d want=%d", path, w.Code, want)
				}
			}
		})
	}
	r := newAuthTestRouter(func(c *gin.Context) { c.Status(204) })
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer forged")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("forged status=%d", w.Code)
	}
}
