package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequirePlatform only routes a signed platform token; identity independently
// verifies the live assignment on every sensitive request.
func RequirePlatform() gin.HandlerFunc {
	return func(c *gin.Context) {
		if admin, _ := c.Get("is_platform_admin"); admin != true {
			c.JSON(http.StatusForbidden, gin.H{"status": "error", "message": "Platform access required", "data": nil})
			c.Abort()
		}
	}
}

// RequireTenant prevents a tenantless platform token from reaching any tenant
// proxy. Parent access retains the pre-existing policy for parent routes.
func RequireTenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := c.Get("tenant_id")
		tenantID, valid := tenant.(string)
		parent, _ := c.Get("is_parent")
		if (!ok || !valid || absentTenantClaim(tenantID)) && parent != true {
			c.JSON(http.StatusForbidden, gin.H{"status": "error", "message": "Tenant access required", "data": nil})
			c.Abort()
		}
	}
}
