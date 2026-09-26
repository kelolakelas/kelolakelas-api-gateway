package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestReadinessDownstreamsAndLiveness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	states := map[string]int{"identity": 200, "academic": 200, "billing": 200}
	servers := make(map[string]*httptest.Server)
	for _, name := range []string{"identity", "academic", "billing"} {
		name := name
		servers[name] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/ready" {
				t.Errorf("path: %s", r.URL.Path)
			}
			w.WriteHeader(states[name])
		}))
		defer servers[name].Close()
	}
	cfg := ReadinessConfig{IdentityURL: servers["identity"].URL, AcademicURL: servers["academic"].URL, BillingURL: servers["billing"].URL}
	r := gin.New()
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "healthy"}) })
	r.GET("/ready", readinessHandler(cfg))
	check := func(path string, code int, detail string) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != code || !strings.Contains(w.Body.String(), detail) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	check("/ready", 200, `"redis":"degraded"`)
	for _, name := range []string{"identity", "academic", "billing"} {
		states[name] = 503
		check("/ready", 503, `"`+name+`":"unavailable"`)
		check("/health", 200, `"status":"healthy"`)
		states[name] = 200
	}
	servers["academic"].Close()
	start := time.Now()
	check("/ready", 503, `"academic":"unavailable"`)
	if time.Since(start) > 3*time.Second {
		t.Fatal("probe exceeded deadline")
	}
}
