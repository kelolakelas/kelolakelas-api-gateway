package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestStripUntrustedContextHeadersRemovesClientContextHeaders proves a client
// cannot smuggle a tenant or an internal service credential past the gateway
// boundary, including when the header is repeated or cased differently.
func TestStripUntrustedContextHeadersRemovesClientContextHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
	}{
		{
			name: "canonical casing",
			headers: map[string]string{
				TenantIDHeader:                  "11111111-1111-1111-1111-111111111111",
				InternalServiceCredentialHeader: "shared-secret",
			},
		},
		{
			name: "non canonical casing",
			headers: map[string]string{
				"x-tenant-id":                   "11111111-1111-1111-1111-111111111111",
				"X-TENANT-ID":                   "22222222-2222-2222-2222-222222222222",
				"x-internal-service-credential": "shared-secret",
				"X-INTERNAL-SERVICE-CREDENTIAL": "other-secret",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(StripUntrustedContextHeaders())

			var sawTenant, sawCredential []string
			router.POST("/probe", func(c *gin.Context) {
				sawTenant = c.Request.Header.Values(TenantIDHeader)
				sawCredential = c.Request.Header.Values(InternalServiceCredentialHeader)
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodPost, "/probe", nil)
			for key, value := range test.headers {
				request.Header.Add(key, value)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status=%d", recorder.Code)
			}
			if len(sawTenant) != 0 {
				t.Fatalf("tenant header survived: %v", sawTenant)
			}
			if len(sawCredential) != 0 {
				t.Fatalf("internal credential header survived: %v", sawCredential)
			}
		})
	}
}

// TestStripUntrustedContextHeadersKeepsUnrelatedHeaders proves the middleware
// removes only the two context headers and leaves the rest of the request
// intact, so proxying still carries the caller's own authorization header.
func TestStripUntrustedContextHeadersKeepsUnrelatedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(StripUntrustedContextHeaders())

	var authorization, requestID string
	router.GET("/probe", func(c *gin.Context) {
		authorization = c.Request.Header.Get("Authorization")
		requestID = c.Request.Header.Get(RequestIDHeader)
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set("Authorization", "Bearer token-value")
	request.Header.Set(RequestIDHeader, "trace-abc")
	request.Header.Set(TenantIDHeader, "11111111-1111-1111-1111-111111111111")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if authorization != "Bearer token-value" {
		t.Fatalf("authorization=%q", authorization)
	}
	if requestID != "trace-abc" {
		t.Fatalf("request id=%q", requestID)
	}
}

// TestAbsentTenantClaim covers both representations of a tenantless claim: an
// absent value and the all-zero UUID the identity service serialises because
// the claim is a fixed-size array that encoding/json cannot omit.
func TestAbsentTenantClaim(t *testing.T) {
	tests := []struct {
		claim string
		want  bool
	}{
		{claim: "", want: true},
		{claim: zeroTenantClaim, want: true},
		{claim: "11111111-1111-1111-1111-111111111111", want: false},
	}
	for _, test := range tests {
		if got := absentTenantClaim(test.claim); got != test.want {
			t.Fatalf("absentTenantClaim(%q)=%v want=%v", test.claim, got, test.want)
		}
	}
}
