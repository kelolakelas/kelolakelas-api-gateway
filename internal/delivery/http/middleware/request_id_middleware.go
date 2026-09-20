package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestIDHeader carries the correlation identifier accepted from clients,
// forwarded to downstream services, and returned on every response.
const RequestIDHeader = "X-Request-ID"

// RequestIDContextKey is the gin context key holding the resolved request ID.
const RequestIDContextKey = "request_id"

const (
	// maxRequestIDLength bounds an accepted inbound identifier so that a
	// client cannot inflate every access log line.
	maxRequestIDLength = 64
	// generatedRequestIDBytes is the entropy of a generated identifier.
	generatedRequestIDBytes = 16
)

// isValidRequestID accepts only short, log-safe identifiers. A value that fails
// this check is replaced with a generated identifier rather than rejected, so a
// malformed client header can never fail a request.
func isValidRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDLength {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '-', character == '_', character == '.':
		default:
			return false
		}
	}
	return true
}

func generateRequestID() string {
	buffer := make([]byte, generatedRequestIDBytes)
	if _, err := rand.Read(buffer); err != nil {
		// crypto/rand is not expected to fail. Keep the request traceable
		// instead of dropping the identifier entirely.
		return "req-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buffer)
}

// RequestIDMiddleware resolves the correlation identifier of a request. It
// reuses a valid inbound header, otherwise generates a new identifier, then
// stores it in the gin context, forwards it to the downstream service, and
// returns it to the client.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader(RequestIDHeader))
		if !isValidRequestID(requestID) {
			requestID = generateRequestID()
		}

		// Mutating the inbound request propagates the identifier through the
		// reverse proxy to the downstream service.
		c.Request.Header.Set(RequestIDHeader, requestID)
		c.Header(RequestIDHeader, requestID)
		c.Set(RequestIDContextKey, requestID)

		c.Next()
	}
}

// RequestIDFromContext returns the identifier resolved for the current request.
func RequestIDFromContext(c *gin.Context) string {
	return c.GetString(RequestIDContextKey)
}
