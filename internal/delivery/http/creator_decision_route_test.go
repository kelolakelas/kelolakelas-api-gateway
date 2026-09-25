package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

func TestCreatorDecisionPlatformRoute(t *testing.T) {
	calls := 0
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			t.Errorf("method=%s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer identity.Close()
	proxy, err := handler.NewProxyHandler(identity.URL, "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(NewRouter(proxy, "decision-secret"))
	defer gateway.Close()
	token := func(claims jwt.MapClaims) string {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
		claims["user_id"] = "00000000-0000-0000-0000-000000000001"
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("decision-secret"))
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	for _, action := range []string{"approve", "reject"} {
		for _, tc := range []struct {
			name, bearer string
			want         int
		}{
			{"anonymous", "", 401},
			{"tenant", token(jwt.MapClaims{"tenant_id": "00000000-0000-0000-0000-000000000002"}), 403},
			{"parent", token(jwt.MapClaims{"is_parent": true}), 403},
			{"platform", token(jwt.MapClaims{"is_platform_admin": true}), 204},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				req, err := http.NewRequest(http.MethodPost, gateway.URL+"/api/v1/platform/creator-requests/00000000-0000-0000-0000-000000000003/"+action, nil)
				if err != nil {
					t.Fatal(err)
				}
				if tc.bearer != "" {
					req.Header.Set("Authorization", "Bearer "+tc.bearer)
				}
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != tc.want {
					t.Fatalf("status=%d want=%d", response.StatusCode, tc.want)
				}
			})
		}
	}
	if calls != 2 {
		t.Fatalf("forwarded=%d want=2", calls)
	}
}
