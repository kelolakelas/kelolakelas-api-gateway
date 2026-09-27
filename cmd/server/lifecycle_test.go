package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/config"
	gatewayhttp "github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http"
	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/handler"
)

const duitkuWebhookPath = "/api/v1/billing/webhooks/duitku"

func lifecycleTestConfig() config.Config {
	return config.Config{
		Port:                    "0",
		ServerReadHeaderTimeout: config.DefaultServerReadHeaderTimeout,
		ServerReadTimeout:       config.DefaultServerReadTimeout,
		ServerWriteTimeout:      config.DefaultServerWriteTimeout,
		ServerIdleTimeout:       config.DefaultServerIdleTimeout,
		ServerShutdownTimeout:   config.DefaultServerShutdownTimeout,
	}
}

// blockingBilling is a billing stub whose webhook handler blocks until released,
// so a proxied request can be held in flight while the gateway shuts down.
type blockingBilling struct {
	server   *httptest.Server
	entered  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

func newBlockingBilling(t *testing.T) *blockingBilling {
	t.Helper()
	b := &blockingBilling{entered: make(chan struct{}, 1), release: make(chan struct{}), canceled: make(chan struct{}, 1)}
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != duitkuWebhookPath {
			http.NotFound(w, r)
			return
		}
		b.entered <- struct{}{}
		select {
		case <-b.release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"success"}`)
		case <-r.Context().Done():
			b.canceled <- struct{}{}
		}
	}))
	// Registered before the gateway's cleanup, so it runs after it: the stub is
	// released and closed once the gateway has stopped.
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
		b.server.Close()
	})
	return b
}

type gatewayLifecycle struct {
	cancel context.CancelFunc
	done   chan error
	addr   string
}

// startGateway runs the real gateway router, proxying to billing, through
// serveUntilDone on a fresh loopback listener. Cancelling the context is what
// SIGINT/SIGTERM does through signal.NotifyContext in main.
func startGateway(t *testing.T, billingURL string, shutdownTimeout time.Duration) *gatewayLifecycle {
	t.Helper()
	unused := "http://127.0.0.1:1"
	proxy, err := handler.NewProxyHandler(unused, unused, billingURL)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &gatewayLifecycle{cancel: cancel, done: make(chan error, 1), addr: listener.Addr().String()}
	server := newHTTPServer(lifecycleTestConfig(), gatewayhttp.NewRouter(proxy, "lifecycle-secret"))
	go func() { l.done <- serveUntilDone(ctx, server, listener, shutdownTimeout) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-l.done:
			l.done <- err
		case <-time.After(5 * time.Second):
			t.Error("serveUntilDone did not return during cleanup")
		}
	})
	return l
}

func (l *gatewayLifecycle) wait(t *testing.T, bound time.Duration) error {
	t.Helper()
	select {
	case err := <-l.done:
		l.done <- err
		return err
	case <-time.After(bound):
		t.Fatalf("serveUntilDone did not return within %s of the shutdown signal", bound)
		return nil
	}
}

// waitRefused polls until a new TCP connection to addr is refused.
func waitRefused(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("new connections to %s were still accepted after shutdown began", addr)
}

type webhookResult struct {
	status int
	body   string
	err    error
}

func postWebhook(addr string) <-chan webhookResult {
	results := make(chan webhookResult, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := client.Post("http://"+addr+duitkuWebhookPath, "application/x-www-form-urlencoded", strings.NewReader("merchantOrderId=abc&resultCode=00"))
		if err != nil {
			results <- webhookResult{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		results <- webhookResult{status: resp.StatusCode, body: string(body), err: err}
	}()
	return results
}

// AC (KEL-71): a proxied request that is in flight when shutdown is triggered
// completes with the downstream response. The case is the public Duitku callback,
// the request a restart must not cut.
func TestShutdownCompletesInFlightProxiedRequest(t *testing.T) {
	billing := newBlockingBilling(t)
	gateway := startGateway(t, billing.server.URL, 5*time.Second)

	responses := postWebhook(gateway.addr)
	select {
	case <-billing.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the webhook never reached billing through the gateway")
	}

	gateway.cancel()
	// New connections are refused as soon as the drain starts; Duitku retries a
	// callback that could not be delivered.
	waitRefused(t, gateway.addr)
	select {
	case err := <-gateway.done:
		t.Fatalf("serveUntilDone returned %v while a proxied request was still in flight", err)
	default:
	}

	close(billing.release)
	got := <-responses
	if got.err != nil || got.status != http.StatusOK || got.body != `{"status":"success"}` {
		t.Fatalf("in-flight webhook status=%d body=%q err=%v, want 200 with the billing body", got.status, got.body, got.err)
	}
	select {
	case <-billing.canceled:
		t.Fatal("shutdown cancelled the proxied request's upstream context")
	default:
	}
	// The drain ends well inside the 5s shutdown timeout, so the process exits early.
	if err := gateway.wait(t, 2*time.Second); err != nil {
		t.Fatalf("graceful shutdown returned %v, want nil", err)
	}
}

// Edge case: a proxied request that outlives the shutdown timeout. The remaining
// connections are closed at the bound and an error is returned, so main exits 1
// instead of hanging until the platform sends SIGKILL.
func TestShutdownForceClosesProxiedRequestThatOutlivesTimeout(t *testing.T) {
	billing := newBlockingBilling(t)
	const shutdownTimeout = 300 * time.Millisecond
	gateway := startGateway(t, billing.server.URL, shutdownTimeout)

	responses := postWebhook(gateway.addr)
	select {
	case <-billing.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the webhook never reached billing through the gateway")
	}

	start := time.Now()
	gateway.cancel()
	err := gateway.wait(t, 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), "HTTP shutdown did not finish in time") {
		t.Fatalf("shutdown error=%v, want the HTTP shutdown timeout", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed < shutdownTimeout || elapsed > 2*time.Second {
		t.Fatalf("shutdown took %s, want about %s", elapsed, shutdownTimeout)
	}
	if got := <-responses; got.err == nil {
		t.Fatalf("the stuck webhook got status=%d, want its connection closed", got.status)
	}
}

// With no request in flight the drain returns at once and reports success.
func TestShutdownWithoutInFlightRequestsReturnsImmediately(t *testing.T) {
	billing := newBlockingBilling(t)
	gateway := startGateway(t, billing.server.URL, 5*time.Second)

	resp, err := http.Get("http://" + gateway.addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	gateway.cancel()
	if err := gateway.wait(t, time.Second); err != nil {
		t.Fatalf("graceful shutdown returned %v, want nil", err)
	}
	waitRefused(t, gateway.addr)
}

// A server that fails while serving is reported rather than swallowed, so main
// exits non-zero.
func TestServeFailureIsReturned(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	done := make(chan error, 1)
	go func() {
		done <- serveUntilDone(context.Background(), newHTTPServer(lifecycleTestConfig(), http.NotFoundHandler()), listener, time.Second)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "serve HTTP") {
			t.Fatalf("error=%v, want the HTTP serve failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveUntilDone kept running after the HTTP server failed")
	}
}
