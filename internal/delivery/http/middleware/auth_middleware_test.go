package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const authTestSecret = "gateway-context-test-secret"

func authTestToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(authTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// newAuthTestRouter mirrors the production chain for the context headers: the
// stripping middleware always runs before the JWT middleware.
func newAuthTestRouter(handle gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(StripUntrustedContextHeaders())
	router.Use(AuthMiddleware(authTestSecret))
	router.GET("/probe", handle)
	return router
}

// TestAuthMiddlewareRejectsUnusableTokens covers the claims the gateway now
// requires: a subject is always mandatory, and a tenant is mandatory for every
// non-parent token. The all-zero UUID counts as a missing tenant rather than as
// a tenant named "zero".
func TestAuthMiddlewareRejectsUnusableTokens(t *testing.T) {
	tests := []struct {
		name   string
		claims jwt.MapClaims
		want   int
	}{
		{
			name:   "token without user_id",
			claims: jwt.MapClaims{"tenant_id": "11111111-1111-1111-1111-111111111111"},
			want:   http.StatusUnauthorized,
		},
		{
			name:   "non-parent token without tenant_id",
			claims: jwt.MapClaims{"user_id": "user-1"},
			want:   http.StatusUnauthorized,
		},
		{
			name:   "non-parent token with zero uuid tenant_id",
			claims: jwt.MapClaims{"user_id": "user-1", "tenant_id": zeroTenantClaim},
			want:   http.StatusUnauthorized,
		},
		{
			name:   "member token with tenant",
			claims: jwt.MapClaims{"user_id": "user-1", "tenant_id": "11111111-1111-1111-1111-111111111111"},
			want:   http.StatusNoContent,
		},
		{
			name:   "parent token without tenant",
			claims: jwt.MapClaims{"user_id": "user-1", "is_parent": true},
			want:   http.StatusNoContent,
		},
		{
			name:   "parent token with zero uuid tenant",
			claims: jwt.MapClaims{"user_id": "user-1", "is_parent": true, "tenant_id": zeroTenantClaim},
			want:   http.StatusNoContent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newAuthTestRouter(func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := httptest.NewRequest(http.MethodGet, "/probe", nil)
			request.Header.Set("Authorization", "Bearer "+authTestToken(t, test.claims))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

// TestAuthMiddlewarePublishesClaimAsTenantHeader proves the tenant header a
// downstream service receives is produced from the verified claim, so a
// client-supplied header can neither survive unchanged nor be duplicated.
func TestAuthMiddlewarePublishesClaimAsTenantHeader(t *testing.T) {
	const claimTenant = "aaaaaaaa-1111-1111-1111-111111111111"
	const clientTenant = "bbbbbbbb-2222-2222-2222-222222222222"

	tests := []struct {
		name        string
		claims      jwt.MapClaims
		clientValue string
		wantHeader  []string
	}{
		{
			name:        "member claim replaces a forged header",
			claims:      jwt.MapClaims{"user_id": "user-1", "tenant_id": claimTenant},
			clientValue: clientTenant,
			wantHeader:  []string{claimTenant},
		},
		{
			name:        "member claim with no forged header",
			claims:      jwt.MapClaims{"user_id": "user-1", "tenant_id": claimTenant},
			clientValue: "",
			wantHeader:  []string{claimTenant},
		},
		{
			name:        "tenantless parent keeps no tenant header",
			claims:      jwt.MapClaims{"user_id": "user-1", "is_parent": true},
			clientValue: clientTenant,
			wantHeader:  nil,
		},
		{
			name:        "parent with zero uuid claim keeps no tenant header",
			claims:      jwt.MapClaims{"user_id": "user-1", "is_parent": true, "tenant_id": zeroTenantClaim},
			clientValue: clientTenant,
			wantHeader:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var observed []string
			router := newAuthTestRouter(func(c *gin.Context) {
				observed = c.Request.Header.Values(TenantIDHeader)
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/probe", nil)
			request.Header.Set("Authorization", "Bearer "+authTestToken(t, test.claims))
			if test.clientValue != "" {
				request.Header.Set(TenantIDHeader, test.clientValue)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if len(observed) != len(test.wantHeader) {
				t.Fatalf("tenant header=%v want=%v", observed, test.wantHeader)
			}
			for index, want := range test.wantHeader {
				if observed[index] != want {
					t.Fatalf("tenant header[%d]=%q want=%q", index, observed[index], want)
				}
			}
		})
	}
}

// TestAuthMiddlewareLeavesNoTenantHeaderWhenClaimIsAbsent proves a tenantless
// token ends up with no tenant header at all, rather than with the all-zero
// claim value, and that the internal service credential is never restored.
func TestAuthMiddlewareLeavesNoTenantHeaderWhenClaimIsAbsent(t *testing.T) {
	var tenantValues, credentialValues []string
	router := newAuthTestRouter(func(c *gin.Context) {
		tenantValues = c.Request.Header.Values(TenantIDHeader)
		credentialValues = c.Request.Header.Values(InternalServiceCredentialHeader)
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set("Authorization", "Bearer "+authTestToken(t, jwt.MapClaims{"user_id": "user-1", "is_parent": true}))
	request.Header.Set(TenantIDHeader, "bbbbbbbb-2222-2222-2222-222222222222")
	request.Header.Set(InternalServiceCredentialHeader, "shared-secret")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(tenantValues) != 0 {
		t.Fatalf("tenant header=%v want none", tenantValues)
	}
	if len(credentialValues) != 0 {
		t.Fatalf("credential header=%v want none", credentialValues)
	}
}
