package http

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
)

func NewRouter(proxyHandler *handler.ProxyHandler, jwtSecret string) *gin.Engine {
	return NewRouterWithConfig(proxyHandler, jwtSecret, "", nil, middleware.RateLimitConfig{}, slog.Default())
}

func NewRouterWithRateLimit(proxyHandler *handler.ProxyHandler, jwtSecret string, redisClient middleware.RedisClient, rateLimitConfig middleware.RateLimitConfig, logger *slog.Logger) *gin.Engine {
	return NewRouterWithConfig(proxyHandler, jwtSecret, "", redisClient, rateLimitConfig, logger)
}

func NewRouterWithConfig(proxyHandler *handler.ProxyHandler, jwtSecret, appURL string, redisClient middleware.RedisClient, rateLimitConfig middleware.RateLimitConfig, logger *slog.Logger) *gin.Engine {
	// The zero ClientIPTrust trusts no proxy, which cannot fail.
	r, _ := NewRouterWithClientIPTrust(proxyHandler, jwtSecret, appURL, redisClient, rateLimitConfig, logger, ClientIPTrust{})
	return r
}

// ClientIPTrust decides where the gateway reads the client IP from (KEL-62).
//
// The client IP keys the rate limiter and is recorded by the access log, so both
// always read it from the same place: gin's ClientIP, configured here once.
//
// With the zero value no proxy is trusted and the client IP is the socket
// address of the peer, whatever headers the request carries. With TrustedProxies
// and Header set, a request whose socket peer lies inside TrustedProxies may name
// the client in Header. The header is read right to left and the first address
// that is not itself a trusted proxy wins, so a caller cannot prepend a forged
// address in front of a trusted chain. An empty header, a value that is not an
// address (including an IPv6 address with a zone), or a peer outside
// TrustedProxies falls back to the socket address.
type ClientIPTrust struct {
	TrustedProxies []string
	Header         string
}

// NewRouterWithClientIPTrust builds the gateway router with an explicit client-IP
// trust policy. It returns an error when a trusted proxy entry is not an IP
// address or CIDR range; configuration loading validates the same rules first.
func NewRouterWithClientIPTrust(proxyHandler *handler.ProxyHandler, jwtSecret, appURL string, redisClient middleware.RedisClient, rateLimitConfig middleware.RateLimitConfig, logger *slog.Logger, trust ClientIPTrust) (*gin.Engine, error) {
	r := gin.New()
	if err := applyClientIPTrust(r, trust); err != nil {
		return nil, err
	}
	// RequestIDMiddleware runs first so every response and log line, including
	// rejected or rate-limited requests, carries a correlation identifier.
	r.Use(middleware.RequestIDMiddleware())
	// Context headers from the client are removed before any route handling, so
	// no middleware or proxy can observe a caller-supplied tenant or service
	// credential. The protected group repopulates the tenant header from the
	// verified claim.
	r.Use(middleware.StripUntrustedContextHeaders())
	r.Use(middleware.AccessLogMiddleware(logger))
	r.Use(gin.Recovery())
	r.Use(middleware.CORSMiddleware(appURL))
	r.Use(middleware.RateLimitMiddleware(redisClient, rateLimitConfig, logger))

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "healthy",
			"service": "api-gateway",
		})
	})

	// Swagger UI routes
	r.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/swagger/index.html")
	})
	r.GET("/swagger/*any", handler.SwaggerUIHandler())
	r.GET("/identity/swagger/*any", proxyHandler.ProxyIdentitySwagger())
	r.GET("/academic/swagger/*any", proxyHandler.ProxyAcademicSwagger())
	r.GET("/billing/swagger/*any", proxyHandler.ProxyBillingSwagger())

	apiV1 := r.Group("/api/v1")
	{
		// Public Auth & Invitation routes - proxying directly to identity-service
		apiV1.POST("/auth/register", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/auth/login", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/platform/auth/login", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/tenants/register", proxyHandler.ProxyToIdentityService())
		apiV1.GET("/invitations/verify", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/invitations/register", proxyHandler.ProxyToIdentityService())
		// Public catalog routes are served by academic-service without authentication.
		apiV1.GET("/catalog/classes", proxyHandler.ProxyToAcademicService())
		apiV1.GET("/catalog/classes/:id", proxyHandler.ProxyToAcademicService())

		// Public Webhook routes - proxying directly to billing-service
		apiV1.POST("/billing/webhooks/duitku", proxyHandler.ProxyToBillingService())

		// Protected routes
		protected := apiV1.Group("")
		protected.Use(middleware.AuthMiddleware(jwtSecret))
		{
			protected.GET("/platform/me", middleware.RequirePlatform(), proxyHandler.ProxyToIdentityService())
			protected.Use(middleware.RequireTenant())
			// Proxied Identity routes
			protected.POST("/invitations", proxyHandler.ProxyToIdentityService())
			protected.GET("/members", proxyHandler.ProxyToIdentityService())
			protected.GET("/tutors", proxyHandler.ProxyToIdentityService())
			protected.GET("/members/:id", proxyHandler.ProxyToIdentityService())
			protected.PUT("/members/:id/role", proxyHandler.ProxyToIdentityService())
			protected.DELETE("/members/:id", proxyHandler.ProxyToIdentityService())
			protected.GET("/tenant/settings", proxyHandler.ProxyToIdentityService())
			protected.PATCH("/tenant/settings", proxyHandler.ProxyToIdentityService())
			protected.GET("/tenants/settings", proxyHandler.ProxyToIdentityService())
			protected.PATCH("/tenants/settings", proxyHandler.ProxyToIdentityService())
			protected.GET("/tenant/settings/location", proxyHandler.ProxyToIdentityService())
			protected.PUT("/tenant/settings/location", proxyHandler.ProxyToIdentityService())
			protected.GET("/permissions", proxyHandler.ProxyToIdentityService())
			protected.GET("/roles", proxyHandler.ProxyToIdentityService())
			protected.POST("/roles", proxyHandler.ProxyToIdentityService())
			protected.PUT("/roles/:id", proxyHandler.ProxyToIdentityService())
			protected.DELETE("/roles/:id", proxyHandler.ProxyToIdentityService())

			// Proxied Academic routes
			protected.POST("/categories", proxyHandler.ProxyToAcademicService())
			protected.GET("/categories", proxyHandler.ProxyToAcademicService())
			protected.DELETE("/categories/:id", proxyHandler.ProxyToAcademicService())
			protected.GET("/classes", proxyHandler.ProxyToAcademicService())
			protected.POST("/classes", proxyHandler.ProxyToAcademicService())
			protected.POST("/classes/with-category", proxyHandler.ProxyToAcademicService())
			protected.DELETE("/classes/:id", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/classes/:id", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/classes/:id/published", proxyHandler.ProxyToAcademicService())
			protected.GET("/students", proxyHandler.ProxyToAcademicService())
			protected.POST("/students", proxyHandler.ProxyToAcademicService())
			protected.GET("/students/:id", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/students/:id", proxyHandler.ProxyToAcademicService())
			protected.DELETE("/students/:id", proxyHandler.ProxyToAcademicService())
			protected.GET("/attendance", proxyHandler.ProxyToAcademicService())
			protected.POST("/attendance", proxyHandler.ProxyToAcademicService())
			protected.GET("/attendance/:id", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/attendance/:id", proxyHandler.ProxyToAcademicService())
			protected.GET("/reports", proxyHandler.ProxyToAcademicService())
			protected.POST("/reports", proxyHandler.ProxyToAcademicService())
			protected.GET("/reports/:id", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/reports/:id", proxyHandler.ProxyToAcademicService())
			protected.DELETE("/reports/:id", proxyHandler.ProxyToAcademicService())

			// Schedule Management routes
			protected.POST("/schedules", proxyHandler.ProxyToAcademicService())
			protected.GET("/schedules", proxyHandler.ProxyToAcademicService())
			protected.DELETE("/schedules/:id", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/permanent", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/:id/permanent", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/schedules/tutor-permanent", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/schedules/:id/tutor-permanent", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/tutor-permanent", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/:id/tutor-permanent", proxyHandler.ProxyToAcademicService())

			// Session Management routes
			protected.GET("/sessions", proxyHandler.ProxyToAcademicService())
			protected.GET("/sessions/:id", proxyHandler.ProxyToAcademicService())
			protected.DELETE("/sessions/:id", proxyHandler.ProxyToAcademicService())
			protected.GET("/sessions/:id/attendees", proxyHandler.ProxyToAcademicService())
			protected.POST("/sessions/reschedule", proxyHandler.ProxyToAcademicService())
			protected.POST("/sessions/:id/reschedule", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/sessions/substitute-tutor", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/sessions/:id/substitute-tutor", proxyHandler.ProxyToAcademicService())

			// Enrollment Management routes
			protected.GET("/enrollments", proxyHandler.ProxyToAcademicService())
			protected.GET("/enrollments/:id", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/enrollments/:id/schedule", proxyHandler.ProxyToAcademicService())
			protected.POST("/enrollments/:id/cancel", proxyHandler.ProxyToAcademicService())
			protected.POST("/tenants/:tenant_id/enrollments", proxyHandler.ProxyToAcademicService())
			protected.POST("/catalog/classes/:class_id/enrollments", proxyHandler.ProxyToAcademicService())

			// Billing Transaction routes
			protected.GET("/billing/transactions", proxyHandler.ProxyToBillingService())
			protected.GET("/billing/transactions/:id", proxyHandler.ProxyToBillingService())
		}
	}

	return r, nil
}

// applyClientIPTrust configures gin's ClientIP from the trust policy. The zero
// policy keeps SetTrustedProxies(nil) with no forwarded header, so every request
// is identified by its socket address exactly as before KEL-62.
func applyClientIPTrust(r *gin.Engine, trust ClientIPTrust) error {
	// Platform headers (Cloudflare, App Engine, …) are never trusted implicitly;
	// they would bypass the proxy check entirely.
	r.TrustedPlatform = ""
	if len(trust.TrustedProxies) == 0 || trust.Header == "" {
		r.ForwardedByClientIP = false
		r.RemoteIPHeaders = nil
		return r.SetTrustedProxies(nil)
	}
	if err := r.SetTrustedProxies(trust.TrustedProxies); err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	r.ForwardedByClientIP = true
	r.RemoteIPHeaders = []string{trust.Header}
	return nil
}
