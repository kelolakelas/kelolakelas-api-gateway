package middleware

import (
	"github.com/gin-gonic/gin"
)

// TenantIDHeader carries the tenant a request acts on. The gateway is the only
// component allowed to populate it, and it does so exclusively from the
// verified JWT claim of the caller.
const TenantIDHeader = "X-Tenant-ID"

// InternalServiceCredentialHeader authenticates direct service-to-service
// calls. It is a service credential, not a client credential, so it must never
// survive a client request that enters through the gateway.
const InternalServiceCredentialHeader = "X-Internal-Service-Credential"

// zeroTenantClaim is the textual form the identity service serialises for a
// token that carries no tenant, for example a parent or a user without an
// active membership.
//
// The identity claim is declared as a uuid.UUID, and encoding/json does not
// apply omitempty to the fixed-size array that type is built on, so the key is
// always present in the payload and a tenantless token publishes the all-zero
// UUID rather than an absent claim. The academic and identity services treat
// that value as "no tenant", so the gateway uses the same rule.
const zeroTenantClaim = "00000000-0000-0000-0000-000000000000"

// absentTenantClaim reports whether a tenant claim fails to name a tenant,
// either because it is empty or because it is the all-zero UUID.
func absentTenantClaim(tenantID string) bool {
	return tenantID == "" || tenantID == zeroTenantClaim
}

// StripUntrustedContextHeaders removes the tenant context and internal service
// credential headers from every request that enters the gateway, before any
// route handling runs.
//
// Without it a caller could nominate the tenant a downstream service acts on,
// or present itself as an internal service, and the gateway - the platform's
// first boundary - would forward the fabricated context untouched. Downstream
// services resolve authorization from the verified claim and not from these
// headers, but a boundary that passes an unverified context header through is
// only safe for as long as every consumer keeps ignoring it.
//
// http.Header.Del canonicalises the key, so differently cased and repeated
// copies of the same header are all removed. The protected route group sets
// the tenant header again from the validated claim through AuthMiddleware,
// which runs after this middleware and never before it.
func StripUntrustedContextHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Header.Del(TenantIDHeader)
		c.Request.Header.Del(InternalServiceCredentialHeader)
		c.Next()
	}
}
