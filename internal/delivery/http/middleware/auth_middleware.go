package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID          string `json:"user_id"`
	Email           string `json:"email"`
	TenantID        string `json:"tenant_id,omitempty"`
	RoleID          string `json:"role_id,omitempty"`
	MemberID        string `json:"member_id,omitempty"`
	IsParent        bool   `json:"is_parent,omitempty"`
	IsPlatformAdmin bool   `json:"is_platform_admin,omitempty"`
	jwt.RegisteredClaims
}

// AuthMiddleware checks the Authorization header for a JWT token
type SessionChecker func(context.Context, string) (int, error)

func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return AuthMiddlewareWithSessionCheck(jwtSecret, nil)
}

func AuthMiddlewareWithSessionCheck(jwtSecret string, check SessionChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": "Unauthorized: Authorization header is required",
				"data":    nil,
			})
			c.Abort()
			return
		}

		// Authorization header format should be Bearer <token>
		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": "Unauthorized: Authorization header format must be Bearer {token}",
				"data":    nil,
			})
			c.Abort()
			return
		}

		tokenStr := parts[1]
		token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(jwtSecret), nil
		})

		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": "Unauthorized: " + err.Error(),
				"data":    nil,
			})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(*Claims)
		if !ok || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": "Unauthorized: Invalid token",
				"data":    nil,
			})
			c.Abort()
			return
		}

		// A token without a subject cannot be authorised for anything, and a
		// non-parent token without a tenant cannot satisfy any tenant-scoped
		// route. Rejecting both here keeps the gateway consistent with the
		// academic service, which applies the same rule.
		if claims.UserID == "" || (!claims.IsParent && !claims.IsPlatformAdmin && absentTenantClaim(claims.TenantID)) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"status":  "error",
				"message": "Unauthorized: Invalid token",
				"data":    nil,
			})
			c.Abort()
			return
		}

		if check != nil {
			status, err := check(c.Request.Context(), tokenStr)
			if err != nil || status >= http.StatusInternalServerError {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"status": "error", "message": "Session validation unavailable", "data": nil})
				return
			}
			if status != http.StatusNoContent {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": "error", "message": "Unauthorized: Invalid session", "data": nil})
				return
			}
		}

		// Set user context in Gin context
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Set("tenant_id", claims.TenantID)
		c.Set("role_id", claims.RoleID)
		c.Set("member_id", claims.MemberID)
		c.Set("is_parent", claims.IsParent)
		c.Set("is_platform_admin", claims.IsPlatformAdmin)

		// Publish the tenant a proxied request acts on. The value comes from the
		// verified claim only, so the header names the identity the token is
		// scoped to and never a tenant the caller selected. A tenantless token
		// (a parent) leaves the header unset; StripUntrustedContextHeaders has
		// already removed any client-supplied value, so downstream receives the
		// header either from this claim or not at all.
		if !absentTenantClaim(claims.TenantID) {
			c.Request.Header.Set(TenantIDHeader, claims.TenantID)
		}

		c.Next()
	}
}
