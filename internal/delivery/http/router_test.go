package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

func TestProtectedRoutesProxyToExpectedService(t *testing.T) {
	services := map[string]*httptest.Server{}
	for _, name := range []string{"identity", "academic", "billing"} {
		serviceName := name
		services[name] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-Service", serviceName)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer services[name].Close()
	}

	proxy, err := handler.NewProxyHandler(services["identity"].URL, services["academic"].URL, services["billing"].URL)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "test-secret")
	gateway := httptest.NewServer(router)
	defer gateway.Close()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   "00000000-0000-0000-0000-000000000001",
		"tenant_id": "00000000-0000-0000-0000-000000000002",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	tokenString, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct{ name, path, service string }{
		{name: "student", path: "/api/v1/students", service: "academic"},
		{name: "enrollment", path: "/api/v1/enrollments", service: "academic"},
		{name: "session", path: "/api/v1/sessions", service: "academic"},
		{name: "session attendees", path: "/api/v1/sessions/00000000-0000-0000-0000-000000000003/attendees", service: "academic"},
		{name: "tutor", path: "/api/v1/tutors", service: "identity"},
		{name: "attendance", path: "/api/v1/attendance", service: "academic"},
		{name: "report", path: "/api/v1/reports", service: "academic"},
		{name: "billing", path: "/api/v1/billing/transactions", service: "billing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, gateway.URL+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+tokenString)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("status=%d", response.StatusCode)
			}
			if got := response.Header.Get("X-Test-Service"); got != test.service {
				t.Fatalf("service=%q want=%q", got, test.service)
			}
		})
	}
}
