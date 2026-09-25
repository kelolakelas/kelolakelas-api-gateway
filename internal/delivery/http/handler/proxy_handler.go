package handler

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
)

// ProxyHandler forwards gateway routes to the downstream services.
//
// Each proxy shares one transport and one upstream deadline, so a downstream
// that accepts a connection and then stops responding releases the caller
// instead of holding the connection open. Failures are converted to the
// platform's JSON envelope; the standard library's error handler answered a
// failed exchange with a plain-text 502 that no client could parse.
type ProxyHandler struct {
	identityServiceURL *url.URL
	academicServiceURL *url.URL
	billingServiceURL  *url.URL
	transport          http.RoundTripper
	upstreamTimeout    time.Duration
	maxBodyBytes       int64
	logger             *slog.Logger
}

// ProxyOptions configures the resilience behaviour shared by every proxy.
//
// A non-positive UpstreamTimeout disables the per-request deadline. Tests use
// that to drive a slow downstream through an injected transport without waiting
// for a real clock. A non-positive MaxBodyBytes keeps the platform default.
type ProxyOptions struct {
	UpstreamTimeout time.Duration
	MaxBodyBytes    int64
	Logger          *slog.Logger
}

func NewProxyHandler(identityServiceAddr, academicServiceAddr, billingServiceAddr string) (*ProxyHandler, error) {
	return NewProxyHandlerWithOptions(identityServiceAddr, academicServiceAddr, billingServiceAddr, ProxyOptions{})
}

// NewProxyHandlerWithOptions builds a proxy handler whose proxied requests are
// bounded by the configured upstream timeout and whose failures are reported as
// JSON envelopes with a status that distinguishes a timeout from an outage.
func NewProxyHandlerWithOptions(identityServiceAddr, academicServiceAddr, billingServiceAddr string, options ProxyOptions) (*ProxyHandler, error) {
	parsedIdentity, err := url.Parse(identityServiceAddr)
	if err != nil {
		return nil, err
	}
	parsedAcademic, err := url.Parse(academicServiceAddr)
	if err != nil {
		return nil, err
	}
	parsedBilling, err := url.Parse(billingServiceAddr)
	if err != nil {
		return nil, err
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ProxyHandler{
		identityServiceURL: parsedIdentity,
		academicServiceURL: parsedAcademic,
		billingServiceURL:  parsedBilling,
		// The default transport is cloned rather than replaced so connection
		// reuse and TLS behaviour stay as the standard library defines them.
		transport:       http.DefaultTransport.(*http.Transport).Clone(),
		upstreamTimeout: options.UpstreamTimeout,
		maxBodyBytes:    options.MaxBodyBytes,
		logger:          logger,
	}, nil
}

// CheckSession asks identity to validate the signed JWT against the durable
// per-user session boundary. An outage is returned to the caller, never cached.
func (h *ProxyHandler) CheckSession(ctx context.Context, signedToken string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.identityServiceURL.String()+"/api/v1/internal/session/check", nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+signedToken)
	client := &http.Client{Transport: h.transport, Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func (h *ProxyHandler) ProxyToIdentityService() gin.HandlerFunc {
	return h.proxyRoute(h.newProxy(h.identityServiceURL), "identity-service")
}

func (h *ProxyHandler) ProxyToAcademicService() gin.HandlerFunc {
	return h.proxyRoute(h.newProxy(h.academicServiceURL), "academic-service")
}

func (h *ProxyHandler) ProxyToBillingService() gin.HandlerFunc {
	return h.proxyRoute(h.newProxy(h.billingServiceURL), "billing-service")
}

// proxyRoute wraps one reverse proxy in the gateway's per-request behaviour: the
// request body limit, the access log target, and the bounded upstream deadline.
func (h *ProxyHandler) proxyRoute(proxy *httputil.ReverseProxy, target string) gin.HandlerFunc {
	bodyLimit := middleware.BodyLimitMiddleware(h.maxBodyBytes)
	return func(c *gin.Context) {
		// The body limit runs first so an oversized request is rejected with 413
		// before any byte is written to a downstream service.
		bodyLimit(c)
		if c.IsAborted() {
			return
		}
		c.Set(middleware.ProxyTargetContextKey, target)
		if h.upstreamTimeout <= 0 {
			proxy.ServeHTTP(c.Writer, c.Request)
			return
		}
		// The deadline covers the whole exchange, including reading the request
		// body and streaming the response, so a downstream cannot keep a caller
		// waiting past the configured bound in any phase.
		ctx, cancel := context.WithTimeout(c.Request.Context(), h.upstreamTimeout)
		defer cancel()
		proxy.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
	}
}

func (h *ProxyHandler) ProxyWithPrefixStrip(targetURL *url.URL, prefixToStrip string) gin.HandlerFunc {
	proxy := h.newProxy(targetURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if prefixToStrip != "" && strings.HasPrefix(req.URL.Path, prefixToStrip) {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, prefixToStrip)
		}
	}

	return h.proxyRoute(proxy, targetURL.Host)
}

func (h *ProxyHandler) ProxyIdentitySwagger() gin.HandlerFunc {
	return h.ProxyWithPrefixStrip(h.identityServiceURL, "/identity")
}

func (h *ProxyHandler) ProxyAcademicSwagger() gin.HandlerFunc {
	return h.ProxyWithPrefixStrip(h.academicServiceURL, "/academic")
}

func (h *ProxyHandler) ProxyBillingSwagger() gin.HandlerFunc {
	return h.ProxyWithPrefixStrip(h.billingServiceURL, "/billing")
}

// newProxy builds a reverse proxy that talks to a downstream through the shared
// transport and reports transport failures through the gateway envelope.
func (h *ProxyHandler) newProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = h.transport
	proxy.ErrorHandler = h.handleProxyError

	hostDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		hostDirector(req)
		req.Host = target.Host
	}
	return proxy
}

// handleProxyError answers a failed upstream exchange with the gateway error
// envelope. A body that exceeded the configured limit is reported as 413, an
// upstream timeout as 504, and every other transport failure, including a
// refused connection, as 502.
//
// The response never carries the transport error text, which could name an
// internal host or port. The error is logged instead, where operators need it.
func (h *ProxyHandler) handleProxyError(writer http.ResponseWriter, request *http.Request, err error) {
	status, message := classifyProxyError(err)

	h.logger.Error("gateway proxy request failed",
		"request_id", request.Header.Get(middleware.RequestIDHeader),
		"method", request.Method,
		"path", request.URL.Path,
		"status", status,
		"error", err,
	)

	middleware.WriteErrorEnvelope(writer, status, message)
}

// classifyProxyError maps a transport failure to the status and the client-safe
// message the gateway returns. It is separate from the handler so the mapping
// can be tested directly.
func classifyProxyError(err error) (int, string) {
	// A body that hit the configured limit is the caller's fault, not the
	// downstream service's, so it must not be reported as a gateway failure.
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge, middleware.BodyTooLargeMessage
	}
	if isTimeoutError(err) {
		return http.StatusGatewayTimeout, "Upstream service timed out"
	}
	return http.StatusBadGateway, "Upstream service is unavailable"
}

// isTimeoutError reports whether a transport failure was caused by a deadline
// or an upstream that stopped responding, as opposed to a refusal or a
// malformed exchange.
func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// A cancellation caused by the caller disconnecting is not an upstream
	// timeout, so it is deliberately not treated as one here.
	return false
}
