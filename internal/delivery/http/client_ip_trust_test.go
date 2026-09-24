package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
)

// countingRedis is a minimal in-memory stand-in for the rate limiter's Redis
// script: it counts per key and reports the window as the TTL.
type countingRedis struct {
	counts map[string]int64
	keys   []string
}

func (client *countingRedis) Eval(ctx context.Context, _ string, keys []string, args ...interface{}) *redis.Cmd {
	if client.counts == nil {
		client.counts = map[string]int64{}
	}
	client.keys = append(client.keys, keys[0])
	client.counts[keys[0]]++
	command := redis.NewCmd(ctx)
	command.SetVal(fmt.Sprintf("%d:%d", client.counts[keys[0]], args[0].(int)))
	return command
}

// loginLimit is the login quota used by these tests: small enough that a few
// requests exhaust it, and equal to the production default.
const loginLimit = 5

type clientIPHarness struct {
	router *routerUnderTest
	redis  *countingRedis
	logs   *bytes.Buffer
}

type routerUnderTest struct{ http.Handler }

func newClientIPHarness(t *testing.T, trust ClientIPTrust) *clientIPHarness {
	t.Helper()
	// The identity upstream answers every login so an accepted request is 204
	// and a rate-limited one is 429.
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(identity.Close)
	proxy, err := handler.NewProxyHandler(identity.URL, "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	redisClient := &countingRedis{}
	var logs bytes.Buffer
	router, err := NewRouterWithClientIPTrust(proxy, "secret", "", redisClient,
		middleware.RateLimitConfig{SensitiveLoginRequests: loginLimit, WindowSeconds: 60},
		slog.New(slog.NewJSONHandler(&logs, nil)), trust)
	if err != nil {
		t.Fatal(err)
	}
	return &clientIPHarness{router: &routerUnderTest{router}, redis: redisClient, logs: &logs}
}

// login sends one login request from the given socket peer with an optional
// X-Forwarded-For value and returns the status.
func (h *clientIPHarness) login(peer, forwardedFor string) int {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	request.RemoteAddr = peer
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	recorder := newCloseNotifyRecorder()
	h.router.ServeHTTP(recorder, request)
	return recorder.Code
}

// lastKeyIP extracts the client IP segment of the most recent rate limit key,
// rate_limit:<ip>:<method>:<path>:<window>. IPv6 addresses contain colons, so the
// fixed suffix is stripped from the right.
func (h *clientIPHarness) lastKeyIP(t *testing.T) string {
	t.Helper()
	if len(h.redis.keys) == 0 {
		t.Fatal("no rate limit key was recorded")
	}
	key := strings.TrimPrefix(h.redis.keys[len(h.redis.keys)-1], "rate_limit:")
	index := strings.LastIndex(key, ":POST:/api/v1/auth/login:")
	if index < 0 {
		t.Fatalf("unexpected rate limit key %q", key)
	}
	return key[:index]
}

// lastLoggedClientIP returns the client_ip of the last access log line.
func (h *clientIPHarness) lastLoggedClientIP(t *testing.T) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(h.logs.String()), "\n")
	var entry struct {
		Msg      string `json:"msg"`
		ClientIP string `json:"client_ip"`
	}
	for index := len(lines) - 1; index >= 0; index-- {
		if err := json.Unmarshal([]byte(lines[index]), &entry); err == nil && entry.Msg == "gateway access" {
			return entry.ClientIP
		}
	}
	t.Fatalf("no access log line: %s", h.logs.String())
	return ""
}

var webServerTrust = ClientIPTrust{TrustedProxies: []string{"10.20.0.0/16"}, Header: "X-Forwarded-For"}

// AC: two requests from a trusted peer naming different clients get separate
// login quotas.
func TestTrustedProxyGivesEachForwardedClientItsOwnLoginQuota(t *testing.T) {
	h := newClientIPHarness(t, webServerTrust)
	const webServer = "10.20.1.5:40000"

	for attempt := 1; attempt <= loginLimit; attempt++ {
		if status := h.login(webServer, "198.51.100.7"); status != http.StatusNoContent {
			t.Fatalf("client A attempt %d status=%d want 204", attempt, status)
		}
	}
	if status := h.login(webServer, "198.51.100.7"); status != http.StatusTooManyRequests {
		t.Fatalf("client A over quota status=%d want 429", status)
	}
	// Same web server, another user: a fresh quota.
	if status := h.login(webServer, "203.0.113.9"); status != http.StatusNoContent {
		t.Fatalf("client B status=%d want 204 on its own quota", status)
	}
	if got := h.lastKeyIP(t); got != "203.0.113.9" {
		t.Fatalf("rate limit key ip=%q want 203.0.113.9", got)
	}
	// Access log and rate limiter agree on the client.
	if got := h.lastLoggedClientIP(t); got != "203.0.113.9" {
		t.Fatalf("access log client_ip=%q want 203.0.113.9", got)
	}
}

// AC + risk mitigation: an untrusted peer rotating forged X-Forwarded-For values
// stays keyed on its socket address and is limited after the quota.
func TestUntrustedPeerCannotRotateForgedForwardedFor(t *testing.T) {
	for name, trust := range map[string]ClientIPTrust{"trust configured": webServerTrust, "default": {}} {
		t.Run(name, func(t *testing.T) {
			h := newClientIPHarness(t, trust)
			const attacker = "192.0.2.44:51000"

			for attempt := 1; attempt <= loginLimit+3; attempt++ {
				forged := fmt.Sprintf("203.0.113.%d", attempt)
				status := h.login(attacker, forged)
				want := http.StatusNoContent
				if attempt > loginLimit {
					want = http.StatusTooManyRequests
				}
				if status != want {
					t.Fatalf("attempt %d with forged %s status=%d want %d", attempt, forged, status, want)
				}
				if got := h.lastKeyIP(t); got != "192.0.2.44" {
					t.Fatalf("attempt %d keyed on %q, want the socket address", attempt, got)
				}
				if got := h.lastLoggedClientIP(t); got != "192.0.2.44" {
					t.Fatalf("attempt %d logged %q, want the socket address", attempt, got)
				}
			}
		})
	}
}

// Without the new variables the key is exactly the pre-KEL-62 key: the socket
// address, even for a peer that would be trusted once configured.
func TestDefaultTrustKeysOnSocketAddress(t *testing.T) {
	h := newClientIPHarness(t, ClientIPTrust{})
	h.login("10.20.1.5:40000", "198.51.100.7")
	if got := h.lastKeyIP(t); got != "10.20.1.5" {
		t.Fatalf("default key ip=%q want the socket address 10.20.1.5", got)
	}
	// The helper used by existing callers is the same untrusting router.
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	if router := NewRouterWithConfig(proxy, "secret", "", nil, middleware.RateLimitConfig{}, slog.Default()); router.ForwardedByClientIP || router.RemoteIPHeaders != nil {
		t.Fatalf("NewRouterWithConfig trusts forwarded headers: forwarded=%v headers=%v", router.ForwardedByClientIP, router.RemoteIPHeaders)
	}
}

// Edge cases from the issue, all from a trusted peer.
func TestTrustedPeerHeaderEdgeCases(t *testing.T) {
	trust := ClientIPTrust{TrustedProxies: []string{"10.20.0.0/16", "2001:db8:1::/48"}, Header: "X-Forwarded-For"}
	for _, test := range []struct {
		name, peer, header, want string
	}{
		{"chain of client and trusted proxies", "10.20.1.5:40000", "198.51.100.7, 10.20.3.3, 10.20.4.4", "198.51.100.7"},
		// A forged leftmost entry is ignored: the first untrusted hop from the right wins.
		{"forged entry in front of the real client", "10.20.1.5:40000", "1.2.3.4, 198.51.100.7, 10.20.3.3", "198.51.100.7"},
		{"missing header", "10.20.1.5:40000", "", "10.20.1.5"},
		{"non-IP value", "10.20.1.5:40000", "not-an-ip", "10.20.1.5"},
		{"IPv6 with zone", "10.20.1.5:40000", "fe80::1%eth0", "10.20.1.5"},
		{"IPv6 client behind an IPv6 proxy", "[2001:db8:1::10]:40000", "2001:db8:ff::7", "2001:db8:ff::7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newClientIPHarness(t, trust)
			h.login(test.peer, test.header)
			if got := h.lastKeyIP(t); got != test.want {
				t.Fatalf("key ip=%q want %q", got, test.want)
			}
			if got := h.lastLoggedClientIP(t); got != test.want {
				t.Fatalf("logged ip=%q want %q", got, test.want)
			}
		})
	}
}

// Only the configured header is read: a trusted peer sending the client in a
// different header is keyed on its socket address, and gin's platform headers
// are never honoured.
func TestOnlyTheConfiguredHeaderIsTrusted(t *testing.T) {
	h := newClientIPHarness(t, ClientIPTrust{TrustedProxies: []string{"10.20.0.0/16"}, Header: "X-Real-IP"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	request.RemoteAddr = "10.20.1.5:40000"
	request.Header.Set("X-Forwarded-For", "198.51.100.7")
	request.Header.Set("CF-Connecting-IP", "198.51.100.8")
	h.router.ServeHTTP(newCloseNotifyRecorder(), request)
	if got := h.lastKeyIP(t); got != "10.20.1.5" {
		t.Fatalf("key ip=%q want the socket address", got)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	request.RemoteAddr = "10.20.1.5:40000"
	request.Header.Set("X-Real-IP", "198.51.100.9")
	h.router.ServeHTTP(newCloseNotifyRecorder(), request)
	if got := h.lastKeyIP(t); got != "198.51.100.9" {
		t.Fatalf("key ip=%q want the X-Real-IP value", got)
	}
}

func TestInvalidTrustedProxyIsRejectedByTheRouter(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewRouterWithClientIPTrust(proxy, "secret", "", nil, middleware.RateLimitConfig{}, slog.Default(),
		ClientIPTrust{TrustedProxies: []string{"10.20.0.0/99"}, Header: "X-Forwarded-For"})
	if err == nil {
		t.Fatal("an invalid trusted proxy range was accepted")
	}
}
