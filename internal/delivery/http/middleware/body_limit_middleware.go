package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DefaultMaxBodyBytes is the request body limit the gateway applies when no
// explicit limit is configured. One mebibyte is far above the largest payload
// any current endpoint receives, including the Duitku callback, which carries
// a fixed set of short string fields, and the tenant registration form. It is
// low enough that a single client cannot buffer an unbounded request in memory.
const DefaultMaxBodyBytes int64 = 1 << 20

// BodyTooLargeMessage is the client-facing message for a rejected body. It is
// shared with the proxy error handler, which reports the same condition when an
// undeclared body is truncated mid-stream instead of being rejected up front.
const BodyTooLargeMessage = "Request body exceeds the configured limit"

// BodyLimitMiddleware rejects a request whose body exceeds maxBytes with the
// gateway's 413 envelope before any handler reads it.
//
// A declared Content-Length over the limit is answered immediately, so an
// oversized upload never reaches a downstream service. A request that declares
// no length, or declares one but sends more, is bounded by
// http.MaxBytesReader, which fails the read as soon as the limit is passed.
// Either way the caller receives JSON rather than a truncated exchange.
func BodyLimitMiddleware(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			AbortWithErrorEnvelope(c, http.StatusRequestEntityTooLarge, BodyTooLargeMessage)
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
