package middleware

import "github.com/gin-gonic/gin"

func CORSMiddleware(allowedOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (allowedOrigin == "" || origin != allowedOrigin) {
			c.AbortWithStatus(403)
			return
		}
		if allowedOrigin != "" && origin == allowedOrigin {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
			c.Header("Vary", "Origin")
		}
		if c.Request.Method == "OPTIONS" {
			c.Status(204)
			c.Abort()
			return
		}
		c.Next()
	}
}
