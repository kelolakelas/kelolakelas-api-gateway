package handler

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

type ProxyHandler struct {
	identityServiceURL *url.URL
	academicServiceURL *url.URL
	billingServiceURL  *url.URL
}

func NewProxyHandler(identityServiceAddr, academicServiceAddr, billingServiceAddr string) (*ProxyHandler, error) {
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
	return &ProxyHandler{
		identityServiceURL: parsedIdentity,
		academicServiceURL: parsedAcademic,
		billingServiceURL:  parsedBilling,
	}, nil
}

func (h *ProxyHandler) ProxyToIdentityService() gin.HandlerFunc {
	proxy := httputil.NewSingleHostReverseProxy(h.identityServiceURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = h.identityServiceURL.Host

		slog.Info("Proxying request to identity-service", "method", req.Method, "path", req.URL.Path)
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func (h *ProxyHandler) ProxyToAcademicService() gin.HandlerFunc {
	proxy := httputil.NewSingleHostReverseProxy(h.academicServiceURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = h.academicServiceURL.Host

		slog.Info("Proxying request to academic-service", "method", req.Method, "path", req.URL.Path)
	}

	return func(c *gin.Context) {
		// Set tenant_id header based on JWT context value
		tenantID := c.GetString("tenant_id")
		if tenantID != "" {
			c.Request.Header.Set("X-Tenant-ID", tenantID)
		}

		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func (h *ProxyHandler) ProxyToBillingService() gin.HandlerFunc {
	proxy := httputil.NewSingleHostReverseProxy(h.billingServiceURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = h.billingServiceURL.Host

		slog.Info("Proxying request to billing-service", "method", req.Method, "path", req.URL.Path)
	}

	return func(c *gin.Context) {
		tenantID := c.GetString("tenant_id")
		if tenantID != "" {
			c.Request.Header.Set("X-Tenant-ID", tenantID)
		}

		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func (h *ProxyHandler) ProxyWithPrefixStrip(targetURL *url.URL, prefixToStrip string) gin.HandlerFunc {
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host
		if prefixToStrip != "" && strings.HasPrefix(req.URL.Path, prefixToStrip) {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, prefixToStrip)
		}
		slog.Info("Proxying request with prefix strip", "target", targetURL.Host, "method", req.Method, "path", req.URL.Path)
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}
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
