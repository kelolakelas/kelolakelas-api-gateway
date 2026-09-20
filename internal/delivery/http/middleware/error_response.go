package middleware

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// errorResponse is the envelope every gateway-generated error uses, so a client
// can parse a failure the same way it parses a proxied downstream error.
//
// The gateway reports its own failures with this envelope instead of the
// standard library's plain-text responses, which carried no status field and
// leaked wording that was never part of the API contract.
type errorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// WriteErrorEnvelope writes the gateway error envelope to a plain response
// writer. It exists for the reverse proxy error handler, which receives the
// underlying writer rather than a gin context.
func WriteErrorEnvelope(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	body, err := json.Marshal(errorResponse{Status: "error", Message: message, Data: nil})
	if err != nil {
		// A struct of strings and nil cannot fail to marshal; the fallback keeps
		// the function total rather than panicking inside an error path.
		_, _ = writer.Write([]byte(`{"status":"error","message":"Request failed","data":null}`))
		return
	}
	_, _ = writer.Write(body)
}

// AbortWithErrorEnvelope writes the gateway error envelope through gin and stops
// the handler chain, which is the middleware equivalent of WriteErrorEnvelope.
func AbortWithErrorEnvelope(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, errorResponse{Status: "error", Message: message, Data: nil})
}
