package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
)

func NewRouter(proxyHandler *handler.ProxyHandler, jwtSecret string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "API Gateway is healthy",
			"data":    nil,
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
		apiV1.POST("/tenants/register", proxyHandler.ProxyToIdentityService())
		apiV1.GET("/invitations/verify", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/invitations/register", proxyHandler.ProxyToIdentityService())

		// Public Webhook routes - proxying directly to billing-service
		apiV1.POST("/billing/webhooks/duitku", proxyHandler.ProxyToBillingService())

		// Protected routes
		protected := apiV1.Group("")
		protected.Use(middleware.AuthMiddleware(jwtSecret))
		{
			// Proxied Identity routes
			protected.POST("/invitations", proxyHandler.ProxyToIdentityService())
			protected.GET("/members", proxyHandler.ProxyToIdentityService())
			protected.GET("/tutors", proxyHandler.ProxyToIdentityService())
			protected.GET("/members/:id", proxyHandler.ProxyToIdentityService())
			protected.PUT("/members/:id/role", proxyHandler.ProxyToIdentityService())
			protected.GET("/tenant/settings", proxyHandler.ProxyToIdentityService())
			protected.PATCH("/tenant/settings", proxyHandler.ProxyToIdentityService())
			protected.GET("/tenants/settings", proxyHandler.ProxyToIdentityService())
			protected.PATCH("/tenants/settings", proxyHandler.ProxyToIdentityService())
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
			protected.GET("/sessions/:id/attendees", proxyHandler.ProxyToAcademicService())
			protected.POST("/sessions/reschedule", proxyHandler.ProxyToAcademicService())
			protected.POST("/sessions/:id/reschedule", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/sessions/substitute-tutor", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/sessions/:id/substitute-tutor", proxyHandler.ProxyToAcademicService())

			// Enrollment Management routes
			protected.GET("/enrollments", proxyHandler.ProxyToAcademicService())
			protected.GET("/enrollments/:id", proxyHandler.ProxyToAcademicService())
			protected.POST("/tenants/:tenant_id/enrollments", proxyHandler.ProxyToAcademicService())
			protected.PUT("/enrollments/:id/status", proxyHandler.ProxyToAcademicService())

			// Billing Transaction routes
			protected.POST("/billing/transactions", proxyHandler.ProxyToBillingService())
			protected.GET("/billing/transactions", proxyHandler.ProxyToBillingService())
			protected.GET("/billing/transactions/:id", proxyHandler.ProxyToBillingService())
		}
	}

	return r
}
