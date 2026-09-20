package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// gatewayErrorEnvelope mirrors the JSON the gateway returns on failure so the
// tests assert the shape clients depend on rather than only the status code.
type gatewayErrorEnvelope struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func TestBodyLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name            string
		limit           int64
		declaredLength  string
		body            string
		expectedStatus  int
		expectHandler   bool
		expectTruncated bool
		expectedMessage string
	}{
		{
			name:           "body under the limit reaches the handler",
			limit:          64,
			body:           `{"address":"Jalan Merdeka 1"}`,
			expectedStatus: http.StatusOK,
			expectHandler:  true,
		},
		{
			name:            "declared length over the limit is rejected before the handler",
			limit:           16,
			body:            strings.Repeat("x", 32),
			expectedStatus:  http.StatusRequestEntityTooLarge,
			expectedMessage: BodyTooLargeMessage,
		},
		{
			// Without a Content-Length the limit cannot be checked before the
			// handler, so the truncation must be detected while the body is read.
			name:            "undeclared length over the limit is truncated while reading",
			limit:           16,
			declaredLength:  "chunked",
			body:            strings.Repeat("x", 32),
			expectedStatus:  http.StatusRequestEntityTooLarge,
			expectHandler:   true,
			expectTruncated: true,
			expectedMessage: BodyTooLargeMessage,
		},
		{
			name:           "exactly the limit is accepted",
			limit:          16,
			body:           strings.Repeat("x", 16),
			expectedStatus: http.StatusOK,
			expectHandler:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var handlerRan bool
			var truncated bool
			router := gin.New()
			router.Use(BodyLimitMiddleware(test.limit))
			router.POST("/api/v1/tenants/register", func(c *gin.Context) {
				handlerRan = true
				// Reading the whole body is what surfaces a truncated undeclared
				// body, mirroring the reverse proxy's behaviour.
				_, err := io.ReadAll(c.Request.Body)
				if err != nil {
					truncated = true
					AbortWithErrorEnvelope(c, http.StatusRequestEntityTooLarge, BodyTooLargeMessage)
					return
				}
				c.Status(http.StatusOK)
			})

			request := httptest.NewRequest(http.MethodPost, "/api/v1/tenants/register", strings.NewReader(test.body))
			if test.declaredLength == "chunked" {
				// A chunked request carries no Content-Length, so the middleware
				// cannot reject it up front and must rely on MaxBytesReader.
				request.ContentLength = -1
				request.TransferEncoding = []string{"chunked"}
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != test.expectedStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.expectedStatus, recorder.Body.String())
			}
			if handlerRan != test.expectHandler {
				t.Fatalf("handler ran=%v want=%v", handlerRan, test.expectHandler)
			}
			if truncated != test.expectTruncated {
				t.Fatalf("body truncated=%v want=%v", truncated, test.expectTruncated)
			}
			if test.expectHandler && recorder.Code == http.StatusOK {
				return
			}
			envelope := gatewayErrorEnvelope{}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error body is not JSON: %v (%s)", err, recorder.Body.String())
			}
			if envelope.Status != "error" || envelope.Message != test.expectedMessage || envelope.Data != nil {
				t.Fatalf("envelope=%+v", envelope)
			}
			if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
				t.Fatalf("content-type=%q", contentType)
			}
		})
	}
}
