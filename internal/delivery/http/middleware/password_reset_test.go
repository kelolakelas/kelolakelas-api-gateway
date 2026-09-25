package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestPasswordResetSensitiveQuota(t *testing.T) {
	config := RateLimitConfig{SensitiveLoginRequests: 3, ProtectedRequests: 100}.withDefaults()
	for _, path := range []string{"/api/v1/auth/password-reset/request", "/api/v1/auth/password-reset/confirm"} {
		limit, _ := config.limitFor(path)
		if limit != 3 {
			t.Fatalf("%s quota=%d", path, limit)
		}
	}
}
func TestGatewaySessionCheckFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	boundary := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		name   string
		iat    int64
		status int
		err    error
		want   int
	}{
		{"old", boundary.Add(-time.Second).Unix(), 401, nil, 401},
		{"new", boundary.Add(time.Second).Unix(), 204, nil, 204},
		{"identity outage", boundary.Unix(), 0, errors.New("network down"), 503},
		{"store outage", boundary.Unix(), 503, nil, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			calls := 0
			r.Use(AuthMiddlewareWithSessionCheck(authTestSecret, func(_ context.Context, signed string) (int, error) {
				calls++
				parsed, err := jwt.ParseWithClaims(signed, &Claims{}, func(*jwt.Token) (interface{}, error) { return []byte(authTestSecret), nil })
				if err != nil {
					t.Fatal(err)
				}
				claims := parsed.Claims.(*Claims)
				if claims.IssuedAt.Time.Before(boundary) {
					return 401, nil
				}
				return tc.status, tc.err
			}))
			r.GET("/protected", func(c *gin.Context) { c.Status(204) })
			token := authTestToken(t, jwt.MapClaims{"user_id": "user-1", "is_parent": true, "iat": tc.iat})
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, request)
			if w.Code != tc.want || calls != 1 {
				t.Fatalf("status=%d want=%d calls=%d", w.Code, tc.want, calls)
			}
		})
	}
}
