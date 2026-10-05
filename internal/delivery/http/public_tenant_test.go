package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

func TestPublicTenantRoute(t *testing.T) {
	path := "/api/v1/tenants/00000000-0000-0000-0000-000000000001/public"
	for _, status := range []int{200, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path || r.Method != "GET" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			defer identity.Close()
			proxy, err := handler.NewProxyHandler(identity.URL, identity.URL, identity.URL)
			if err != nil {
				t.Fatal(err)
			}
			router := NewRouter(proxy, "secret")
			w := newCloseNotifyRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != status {
				t.Fatalf("status %d, want %d", w.Code, status)
			}
		})
	}
}
