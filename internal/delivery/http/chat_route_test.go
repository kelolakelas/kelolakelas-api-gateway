package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
)

// ginMode pins gin to test mode for one test.
func ginMode(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
}

// newBareWSRouter wires only the chat WS handler, without the global CORS
// middleware, so the foreign-origin test proves the explicit check inside
// ProxyToChatWS rather than the router-level CORS rejection.
func newBareWSRouter(proxy *handler.ProxyHandler, appURL string) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/chat/ws", proxy.ProxyToChatWS(appURL))
	return r
}

// newReadinessRouter wires only the readiness handler for focused tests.
func newReadinessRouter(cfg ReadinessConfig) *gin.Engine {
	r := gin.New()
	r.GET("/ready", readinessHandler(cfg))
	return r
}

// chatTestSecret signs the tokens the chat route tests use.
const chatTestSecret = "chat-route-test-secret"

const chatMemberTenant = "00000000-0000-0000-0000-000000000002"

func signChatToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	if _, ok := claims["user_id"]; !ok {
		claims["user_id"] = "00000000-0000-0000-0000-000000000001"
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(chatTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func chatMemberToken(t *testing.T) string {
	t.Helper()
	return signChatToken(t, jwt.MapClaims{"tenant_id": chatMemberTenant})
}

func chatParentToken(t *testing.T) string {
	t.Helper()
	return signChatToken(t, jwt.MapClaims{"is_parent": true})
}

// chatRESTRoutes lists every protected REST chat route the gateway must expose
// (KEL-122), mirroring the paths chat-service serves. wantPath is the gin
// route pattern; requestPath is a concrete path used to drive requests.
var chatRESTRoutes = []struct {
	name, method, wantPath, requestPath string
}{
	{"list conversations", http.MethodGet, "/api/v1/chat/conversations", "/api/v1/chat/conversations"},
	{"create conversation", http.MethodPost, "/api/v1/chat/conversations", "/api/v1/chat/conversations"},
	{"get conversation", http.MethodGet, "/api/v1/chat/conversations/:id", "/api/v1/chat/conversations/00000000-0000-0000-0000-000000000003"},
	{"list messages", http.MethodGet, "/api/v1/chat/conversations/:id/messages", "/api/v1/chat/conversations/00000000-0000-0000-0000-000000000003/messages"},
	{"send message", http.MethodPost, "/api/v1/chat/conversations/:id/messages", "/api/v1/chat/conversations/00000000-0000-0000-0000-000000000003/messages"},
	{"mark read", http.MethodPost, "/api/v1/chat/conversations/:id/read", "/api/v1/chat/conversations/00000000-0000-0000-0000-000000000003/read"},
	{"issue ws ticket", http.MethodPost, "/api/v1/chat/ws-tickets", "/api/v1/chat/ws-tickets"},
}

// chatUpstream records what a fake chat-service observed.
type chatUpstream struct {
	calls  atomic.Int64
	path   atomic.Value
	method atomic.Value
	auth   atomic.Value
	tenant atomic.Value
}

func newChatUpstream(status int) (*chatUpstream, *httptest.Server) {
	observed := &chatUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed.calls.Add(1)
		observed.path.Store(r.URL.Path)
		observed.method.Store(r.Method)
		observed.auth.Store(r.Header.Get("Authorization"))
		observed.tenant.Store(r.Header.Get("X-Tenant-ID"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"success","message":"OK","data":null}`))
	}))
	return observed, server
}

func chatProxy(t *testing.T, chatURL string, options handler.ProxyOptions) *handler.ProxyHandler {
	t.Helper()
	options.ChatServiceURL = chatURL
	proxy, err := handler.NewProxyHandlerWithOptions("http://identity", "http://academic", "http://billing", options)
	if err != nil {
		t.Fatal(err)
	}
	return proxy
}

// TestChatRESTRoutesAreRegistered proves every protected REST chat route exists
// on the router, so a later route-table edit cannot silently drop one.
func TestChatRESTRoutesAreRegistered(t *testing.T) {
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, route := range NewRouter(proxy, chatTestSecret).Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range chatRESTRoutes {
		if !registered[route.method+" "+route.wantPath] {
			t.Errorf("route %s %s is not registered", route.method, route.wantPath)
		}
	}
	if !registered[http.MethodGet+" /api/v1/chat/ws"] {
		t.Error("route GET /api/v1/chat/ws is not registered")
	}
}

// TestChatRESTRoutesAreProtectedChatProxies proves each REST chat route rejects
// an anonymous caller with 401 before touching chat-service, and forwards an
// authenticated member or parent caller with path, method and tenant intact.
func TestChatRESTRoutesAreProtectedChatProxies(t *testing.T) {
	observed, chat := newChatUpstream(http.StatusOK)
	defer chat.Close()
	router := NewRouter(chatProxy(t, chat.URL, handler.ProxyOptions{}), chatTestSecret)

	for _, route := range chatRESTRoutes {
		t.Run(route.name, func(t *testing.T) {
			before := observed.calls.Load()
			anonymous := httptest.NewRecorder()
			router.ServeHTTP(anonymous, httptest.NewRequest(route.method, route.requestPath, nil))
			if anonymous.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous status=%d want=%d", anonymous.Code, http.StatusUnauthorized)
			}
			if observed.calls.Load() != before {
				t.Fatal("anonymous request reached chat-service")
			}

			for _, principal := range []struct {
				name, token, wantTenant string
			}{
				{"member", "Bearer " + chatMemberToken(t), chatMemberTenant},
				{"parent", "Bearer " + chatParentToken(t), ""},
			} {
				t.Run(principal.name, func(t *testing.T) {
					request := httptest.NewRequest(route.method, route.requestPath, strings.NewReader(`{}`))
					request.Header.Set("Authorization", principal.token)
					recorder := newCloseNotifyRecorder()
					router.ServeHTTP(recorder, request)
					if recorder.Code != http.StatusOK {
						t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
					}
					if got := observed.path.Load().(string); got != route.requestPath {
						t.Fatalf("downstream path=%q want=%q", got, route.requestPath)
					}
					if got := observed.method.Load().(string); got != route.method {
						t.Fatalf("downstream method=%q want=%q", got, route.method)
					}
					if got := observed.auth.Load().(string); got != principal.token {
						t.Fatalf("downstream authorization=%q want the caller token", got)
					}
					if got := observed.tenant.Load().(string); got != principal.wantTenant {
						t.Fatalf("downstream tenant=%q want=%q", got, principal.wantTenant)
					}
				})
			}
		})
	}
}

// chatEchoUpstream is a fake chat-service WebSocket endpoint: it upgrades every
// handshake and echoes each message back.
func chatEchoUpstream(t *testing.T, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "not a websocket handshake", http.StatusBadRequest)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, message); err != nil {
				return
			}
		}
	}))
}

func gatewayWSURL(gatewayURL string) string {
	return "ws://" + strings.TrimPrefix(gatewayURL, "http://") + "/api/v1/chat/ws?ticket=dummy"
}

// TestChatWSUpgradeSurvivesUpstreamTimeout is the KEL-122 timeout-risk
// mitigation: with the shared upstream timeout shrunk to half a second, a REST
// call to a stalled chat-service still fails fast with 504 (the timeout is
// armed), while an upgraded WebSocket stays connected well past that deadline
// and still echoes.
func TestChatWSUpgradeSurvivesUpstreamTimeout(t *testing.T) {
	ginMode(t)
	const upstreamTimeout = 500 * time.Millisecond
	const appURL = "http://app.example.test"

	t.Run("rest still bounded by the upstream timeout", func(t *testing.T) {
		stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer stalled.Close()
		router := NewRouter(chatProxy(t, stalled.URL, handler.ProxyOptions{UpstreamTimeout: upstreamTimeout}), chatTestSecret)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/chat/conversations", nil)
		request.Header.Set("Authorization", "Bearer "+chatMemberToken(t))
		recorder := newCloseNotifyRecorder()
		start := time.Now()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusGatewayTimeout {
			t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusGatewayTimeout, recorder.Body.String())
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("request was not bounded by the shrunk timeout: %s", elapsed)
		}
	})

	t.Run("websocket outlives the upstream timeout", func(t *testing.T) {
		var calls atomic.Int64
		echo := chatEchoUpstream(t, &calls)
		defer echo.Close()
		router := NewRouterWithConfig(
			chatProxy(t, echo.URL, handler.ProxyOptions{UpstreamTimeout: upstreamTimeout}),
			chatTestSecret, appURL, nil, middleware.RateLimitConfig{}, slog.Default(),
		)
		gateway := httptest.NewServer(router)
		defer gateway.Close()

		header := http.Header{}
		header.Set("Origin", appURL)
		conn, response, err := websocket.DefaultDialer.Dial(gatewayWSURL(gateway.URL), header)
		if err != nil {
			if response != nil {
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				t.Fatalf("dial: %v status=%d body=%s", err, response.StatusCode, body)
			}
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if calls.Load() != 1 {
			t.Fatalf("upstream calls=%d want=1", calls.Load())
		}

		// Sleep past twice the shrunk upstream timeout: a gateway that applied
		// the REST deadline to the hijacked socket would have cut it by now.
		time.Sleep(1200 * time.Millisecond)
		if err := conn.WriteMessage(websocket.TextMessage, []byte("kel-122")); err != nil {
			t.Fatalf("write after timeout window: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read after timeout window: %v", err)
		}
		if messageType != websocket.TextMessage || string(message) != "kel-122" {
			t.Fatalf("echo type=%d message=%q", messageType, message)
		}
	})
}

// TestChatWSForeignOriginRejected proves an upgrade carrying a foreign Origin
// is rejected with 403 before chat-service is contacted: once through the full
// router (global CORS), and once against the handler alone without CORS, which
// proves the explicit check inside ProxyToChatWS.
func TestChatWSForeignOriginRejected(t *testing.T) {
	ginMode(t)
	const appURL = "http://app.example.test"

	t.Run("through the gateway router", func(t *testing.T) {
		var calls atomic.Int64
		echo := chatEchoUpstream(t, &calls)
		defer echo.Close()
		router := NewRouterWithConfig(
			chatProxy(t, echo.URL, handler.ProxyOptions{}),
			chatTestSecret, appURL, nil, middleware.RateLimitConfig{}, slog.Default(),
		)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws?ticket=dummy", nil)
		request.Header.Set("Origin", "https://evil.example.com")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d want=%d", recorder.Code, http.StatusForbidden)
		}
		if calls.Load() != 0 {
			t.Fatalf("foreign origin reached chat-service %d times", calls.Load())
		}
	})

	t.Run("handler without cors", func(t *testing.T) {
		var calls atomic.Int64
		echo := chatEchoUpstream(t, &calls)
		defer echo.Close()
		proxy := chatProxy(t, echo.URL, handler.ProxyOptions{})
		bare := newBareWSRouter(proxy, appURL)
		gateway := httptest.NewServer(bare)
		defer gateway.Close()

		foreign := http.Header{}
		foreign.Set("Origin", "https://evil.example.com")
		conn, response, err := websocket.DefaultDialer.Dial(gatewayWSURL(gateway.URL), foreign)
		if err == nil {
			conn.Close()
			t.Fatal("foreign origin upgrade succeeded")
		}
		if response == nil || response.StatusCode != http.StatusForbidden {
			status := 0
			if response != nil {
				status = response.StatusCode
				response.Body.Close()
			}
			t.Fatalf("foreign origin status=%d want=%d", status, http.StatusForbidden)
		}
		response.Body.Close()
		if calls.Load() != 0 {
			t.Fatalf("foreign origin reached chat-service %d times", calls.Load())
		}

		for _, origin := range []struct{ name, value string }{
			{"matching origin", appURL},
			{"no origin", ""},
		} {
			t.Run(origin.name, func(t *testing.T) {
				header := http.Header{}
				if origin.value != "" {
					header.Set("Origin", origin.value)
				}
				conn, response, err := websocket.DefaultDialer.Dial(gatewayWSURL(gateway.URL), header)
				if err != nil {
					if response != nil {
						body, _ := io.ReadAll(response.Body)
						response.Body.Close()
						t.Fatalf("dial: %v status=%d body=%s", err, response.StatusCode, body)
					}
					t.Fatalf("dial: %v", err)
				}
				defer conn.Close()
				if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
					t.Fatalf("write: %v", err)
				}
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				if _, message, err := conn.ReadMessage(); err != nil || string(message) != "ping" {
					t.Fatalf("echo message=%q err=%v", message, err)
				}
			})
		}
	})
}

// TestChatRoutesAnswer503WithoutChatURL proves AC 4: with CHAT_SERVICE_URL
// empty every chat route answers 503 with the gateway envelope, while a
// non-chat route keeps proxying.
func TestChatRoutesAnswer503WithoutChatURL(t *testing.T) {
	ginMode(t)
	proxy, err := handler.NewProxyHandler("http://identity", "http://academic", "http://billing")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(proxy, chatTestSecret)

	assertChatUnavailable := func(t *testing.T, code int, body string) {
		t.Helper()
		if code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d want=%d body=%s", code, http.StatusServiceUnavailable, body)
		}
		assertChatEnvelope(t, []byte(body), "Chat service is unavailable")
	}

	for _, route := range chatRESTRoutes {
		t.Run(route.name, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.requestPath, strings.NewReader(`{}`))
			request.Header.Set("Authorization", "Bearer "+chatMemberToken(t))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			assertChatUnavailable(t, recorder.Code, recorder.Body.String())
		})
	}

	t.Run("websocket upgrade", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/chat/ws?ticket=dummy", nil))
		assertChatUnavailable(t, recorder.Code, recorder.Body.String())
	})

	t.Run("non-chat route unaffected", func(t *testing.T) {
		academic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer academic.Close()
		academicProxy, err := handler.NewProxyHandler("http://identity", academic.URL, "http://billing")
		if err != nil {
			t.Fatal(err)
		}
		academicRouter := NewRouter(academicProxy, chatTestSecret)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/students", nil)
		request.Header.Set("Authorization", "Bearer "+chatMemberToken(t))
		recorder := newCloseNotifyRecorder()
		academicRouter.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status=%d want=%d", recorder.Code, http.StatusNoContent)
		}
	})
}

// TestChatWSUpstreamDownReturns502 proves a chat-service that is down at
// upgrade time surfaces as a 502 gateway envelope, never a hung handshake.
func TestChatWSUpstreamDownReturns502(t *testing.T) {
	ginMode(t)
	const appURL = "http://app.example.test"
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	router := NewRouterWithConfig(
		chatProxy(t, deadURL, handler.ProxyOptions{UpstreamTimeout: 5 * time.Second}),
		chatTestSecret, appURL, nil, middleware.RateLimitConfig{}, slog.Default(),
	)
	gateway := httptest.NewServer(router)
	defer gateway.Close()

	header := http.Header{}
	header.Set("Origin", appURL)
	_, response, err := websocket.DefaultDialer.Dial(gatewayWSURL(gateway.URL), header)
	if err == nil {
		t.Fatal("upgrade against a dead upstream succeeded")
	}
	if response == nil {
		t.Fatalf("dial error without a response: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d want=%d", response.StatusCode, http.StatusBadGateway)
	}
	body, _ := io.ReadAll(response.Body)
	assertChatEnvelope(t, body, "Upstream service is unavailable")
	if strings.Contains(string(body), strings.TrimPrefix(deadURL, "http://")) {
		t.Fatalf("error envelope leaks the internal address: %s", body)
	}
}

// TestChatRESTBodyTooLarge proves the shared body limit guards the chat
// routes: an oversized message is rejected with 413 before chat-service sees
// it, while an in-limit request still proxies.
func TestChatRESTBodyTooLarge(t *testing.T) {
	const maxBodyBytes = 1024
	observed, chat := newChatUpstream(http.StatusNoContent)
	defer chat.Close()
	router := NewRouter(chatProxy(t, chat.URL, handler.ProxyOptions{
		UpstreamTimeout: 5 * time.Second,
		MaxBodyBytes:    maxBodyBytes,
	}), chatTestSecret)

	payload := `{"body":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/00000000-0000-0000-0000-000000000003/messages", strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+chatMemberToken(t))
	recorder := newCloseNotifyRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
	assertChatEnvelope(t, recorder.Body.Bytes(), middleware.BodyTooLargeMessage)
	if observed.calls.Load() != 0 {
		t.Fatalf("oversized request reached chat-service %d times", observed.calls.Load())
	}

	accepted := newCloseNotifyRecorder()
	acceptedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/chat/conversations/00000000-0000-0000-0000-000000000003/messages", strings.NewReader(`{"body":"hi"}`))
	acceptedRequest.Header.Set("Authorization", "Bearer "+chatMemberToken(t))
	router.ServeHTTP(accepted, acceptedRequest)
	if accepted.Code != http.StatusNoContent {
		t.Fatalf("in-limit status=%d want=%d body=%s", accepted.Code, http.StatusNoContent, accepted.Body.String())
	}
}

// TestReadinessChatProbeOptional proves the chat readiness check only runs
// when configured: absent ChatURL leaves the components untouched, a healthy
// chat-service reports healthy at its /health endpoint (chat-service serves no
// /ready), and a sick one fails the probe.
func TestReadinessChatProbeOptional(t *testing.T) {
	ginMode(t)
	ready := func(status int, path string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(status)
		}))
	}
	identity := ready(http.StatusOK, "/ready")
	defer identity.Close()
	academic := ready(http.StatusOK, "/ready")
	defer academic.Close()
	billing := ready(http.StatusOK, "/ready")
	defer billing.Close()

	check := func(t *testing.T, cfg ReadinessConfig, code int, detail string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		newReadinessRouter(cfg).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if recorder.Code != code || !strings.Contains(recorder.Body.String(), detail) {
			t.Fatalf("status=%d want=%d body=%s", recorder.Code, code, recorder.Body.String())
		}
	}

	base := ReadinessConfig{IdentityURL: identity.URL, AcademicURL: academic.URL, BillingURL: billing.URL}
	t.Run("absent chat url is ignored", func(t *testing.T) {
		check(t, base, http.StatusOK, `"redis":"degraded"`)
		recorder := httptest.NewRecorder()
		newReadinessRouter(base).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if strings.Contains(recorder.Body.String(), `"chat"`) {
			t.Fatalf("unconfigured chat appears in readiness: %s", recorder.Body.String())
		}
	})

	t.Run("healthy chat reports healthy", func(t *testing.T) {
		chat := ready(http.StatusOK, "/health")
		defer chat.Close()
		cfg := base
		cfg.ChatURL = chat.URL
		check(t, cfg, http.StatusOK, `"chat":"healthy"`)
	})

	t.Run("sick chat fails readiness", func(t *testing.T) {
		chat := ready(http.StatusServiceUnavailable, "/health")
		defer chat.Close()
		cfg := base
		cfg.ChatURL = chat.URL
		check(t, cfg, http.StatusServiceUnavailable, `"chat":"unavailable"`)
	})
}

func assertChatEnvelope(t *testing.T, body []byte, wantMessage string) {
	t.Helper()
	var envelope struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, body)
	}
	if envelope.Status != "error" {
		t.Fatalf("status field=%q body=%s", envelope.Status, body)
	}
	if envelope.Message != wantMessage {
		t.Fatalf("message=%q want=%q", envelope.Message, wantMessage)
	}
	if envelope.Data != nil {
		t.Fatalf("data=%v want null", envelope.Data)
	}
}
