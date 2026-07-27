package handler

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gin-gonic/gin"
)

type ProxyHandler struct {
	identityServiceURL *url.URL
	academicServiceURL *url.URL
}

func NewProxyHandler(identityServiceAddr, academicServiceAddr string) (*ProxyHandler, error) {
	parsedIdentity, err := url.Parse(identityServiceAddr)
	if err != nil {
		return nil, err
	}
	parsedAcademic, err := url.Parse(academicServiceAddr)
	if err != nil {
		return nil, err
	}
	return &ProxyHandler{
		identityServiceURL: parsedIdentity,
		academicServiceURL: parsedAcademic,
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
