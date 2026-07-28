package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tutorin-id/tutorin-api-gateway/internal/delivery/http/handler"
	"github.com/tutorin-id/tutorin-api-gateway/internal/delivery/http/middleware"
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

	apiV1 := r.Group("/api/v1")
	{
		// Public Auth & Invitation routes - proxying directly to identity-service
		apiV1.POST("/auth/register", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/auth/login", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/tenants/register", proxyHandler.ProxyToIdentityService())
		apiV1.GET("/invitations/verify", proxyHandler.ProxyToIdentityService())
		apiV1.POST("/invitations/register", proxyHandler.ProxyToIdentityService())

		// Protected routes
		protected := apiV1.Group("")
		protected.Use(middleware.AuthMiddleware(jwtSecret))
		{
			// Proxied Identity routes
			protected.POST("/invitations", proxyHandler.ProxyToIdentityService())
			protected.GET("/permissions", proxyHandler.ProxyToIdentityService())
			protected.GET("/roles", proxyHandler.ProxyToIdentityService())
			protected.POST("/roles", proxyHandler.ProxyToIdentityService())
			protected.PUT("/roles/:id", proxyHandler.ProxyToIdentityService())
			protected.DELETE("/roles/:id", proxyHandler.ProxyToIdentityService())

			// Dummy protected route to demonstrate functionality
			protected.GET("/protected-demo", func(c *gin.Context) {
				userID, _ := c.Get("user_id")
				email, _ := c.Get("email")

				c.JSON(http.StatusOK, gin.H{
					"status":  "success",
					"message": "You have accessed a protected route via API Gateway!",
					"data": gin.H{
						"user_id": userID,
						"email":   email,
					},
				})
			})

			// Proxied Academic routes
			protected.POST("/categories", proxyHandler.ProxyToAcademicService())
			protected.POST("/classes", proxyHandler.ProxyToAcademicService())

			// Schedule Management routes
			protected.POST("/schedules", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/permanent", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/:id/permanent", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/schedules/tutor-permanent", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/schedules/:id/tutor-permanent", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/tutor-permanent", proxyHandler.ProxyToAcademicService())
			protected.PUT("/schedules/:id/tutor-permanent", proxyHandler.ProxyToAcademicService())

			// Session Management routes
			protected.POST("/sessions/reschedule", proxyHandler.ProxyToAcademicService())
			protected.POST("/sessions/:id/reschedule", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/sessions/substitute-tutor", proxyHandler.ProxyToAcademicService())
			protected.PATCH("/sessions/:id/substitute-tutor", proxyHandler.ProxyToAcademicService())
		}
	}

	return r
}
