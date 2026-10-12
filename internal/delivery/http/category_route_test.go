package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

// KEL-188: PATCH /api/v1/categories/:id is a protected tenant route proxied to
// the academic service. The gateway never serves it unauthenticated.
func TestCategoryRoutesProxyProtectedPatch(t *testing.T) {
	var path, method string
	academic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, method = r.URL.Path, r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer academic.Close()
	proxy, err := handler.NewProxyHandler("http://identity", academic.URL, "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, "secret")
	const update = "/api/v1/categories/00000000-0000-0000-0000-000000000001"

	blocked := httptest.NewRecorder()
	router.ServeHTTP(blocked, httptest.NewRequest(http.MethodPatch, update, nil))
	if blocked.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated patch: %d", blocked.Code)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   "00000000-0000-0000-0000-000000000002",
		"tenant_id": "00000000-0000-0000-0000-000000000003",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, update, nil)
	request.Header.Set("Authorization", "Bearer "+signed)
	allowed := newCloseNotifyRecorder()
	router.ServeHTTP(allowed, request)
	if allowed.Code != http.StatusNoContent || path != update || method != http.MethodPatch {
		t.Fatalf("authorized: %d %s %s", allowed.Code, method, path)
	}
}
