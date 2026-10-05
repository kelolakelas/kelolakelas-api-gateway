package http

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRefundGatewayRoute(t *testing.T) {
	calls := 0
	billing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/api/v1/billing/transactions/00000000-0000-0000-0000-000000000003/refund" || string(body) != `{"reason":"refund","transfer_reference":"bank-001"}` {
			t.Errorf("forwarded request %s %s %s", r.Method, r.URL.Path, body)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing authorization")
		}
		w.WriteHeader(200)
	}))
	defer billing.Close()
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", billing.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewRouter(proxy, "refund-secret"))
	defer server.Close()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": "00000000-0000-0000-0000-000000000001", "tenant_id": "00000000-0000-0000-0000-000000000002", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("refund-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bearer := range []string{"", signed} {
		r, err := http.NewRequest("POST", server.URL+"/api/v1/billing/transactions/00000000-0000-0000-0000-000000000003/refund", strings.NewReader(`{"reason":"refund","transfer_reference":"bank-001"}`))
		if err != nil {
			t.Fatal(err)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(w.Body)
		w.Body.Close()
		want := 401
		if bearer != "" {
			want = 200
		}
		if w.StatusCode != want {
			t.Fatalf("status=%d want=%d body=%s", w.StatusCode, want, body)
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
