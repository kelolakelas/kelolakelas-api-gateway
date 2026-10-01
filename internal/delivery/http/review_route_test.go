package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

func TestReviewRoutesProxyPublicReadAndProtectedWrite(t *testing.T) {
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
	const read = "/api/v1/catalog/classes/00000000-0000-0000-0000-000000000001/reviews"
	const write = "/api/v1/enrollments/00000000-0000-0000-0000-000000000001/review"
	public := newCloseNotifyRecorder()
	router.ServeHTTP(public, httptest.NewRequest(http.MethodGet, read, nil))
	if public.Code != http.StatusNoContent || path != read || method != http.MethodGet {
		t.Fatalf("public: %d %s %s", public.Code, method, path)
	}
	blocked := httptest.NewRecorder()
	router.ServeHTTP(blocked, httptest.NewRequest(http.MethodPut, write, nil))
	if blocked.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated write: %d", blocked.Code)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "00000000-0000-0000-0000-000000000001", "is_parent": true, "exp": time.Now().Add(time.Hour).Unix()})
	signed, err := token.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, write, nil)
	request.Header.Set("Authorization", "Bearer "+signed)
	allowed := newCloseNotifyRecorder()
	router.ServeHTTP(allowed, request)
	if allowed.Code != http.StatusNoContent || path != write || method != http.MethodPut {
		t.Fatalf("authorized: %d %s %s", allowed.Code, method, path)
	}
}
