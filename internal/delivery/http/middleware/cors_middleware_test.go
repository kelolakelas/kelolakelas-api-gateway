package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCORSMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name          string
		method        string
		origin        string
		expectedCode  int
		expectedAllow string
	}{
		{name: "allows configured origin", method: http.MethodGet, origin: "https://frontend.example.com", expectedCode: http.StatusOK, expectedAllow: "https://frontend.example.com"},
		{name: "rejects other origin", method: http.MethodGet, origin: "https://other.example.com", expectedCode: http.StatusForbidden},
		{name: "allows preflight", method: http.MethodOptions, origin: "https://frontend.example.com", expectedCode: http.StatusNoContent, expectedAllow: "https://frontend.example.com"},
		{name: "rejects foreign preflight", method: http.MethodOptions, origin: "https://other.example.com", expectedCode: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(CORSMiddleware("https://frontend.example.com"))
			router.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, "/health", nil)
			request.Header.Set("Origin", test.origin)
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.expectedCode {
				t.Fatalf("status=%d, expected %d", recorder.Code, test.expectedCode)
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != test.expectedAllow {
				t.Fatalf("allow-origin=%q, expected %q", got, test.expectedAllow)
			}
		})
	}
}
