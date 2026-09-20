package handler

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/kelolakelas/kelolakelas-api-gateway/internal/delivery/http/middleware"
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
	}

	return func(c *gin.Context) {
		c.Set(middleware.ProxyTargetContextKey, "identity-service")
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func (h *ProxyHandler) ProxyToAcademicService() gin.HandlerFunc {
	proxy := httputil.NewSingleHostReverseProxy(h.academicServiceURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = h.academicServiceURL.Host
	}

	return func(c *gin.Context) {
		c.Set(middleware.ProxyTargetContextKey, "academic-service")
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func (h *ProxyHandler) ProxyToBillingService() gin.HandlerFunc {
	proxy := httputil.NewSingleHostReverseProxy(h.billingServiceURL)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = h.billingServiceURL.Host
	}

	return func(c *gin.Context) {
		c.Set(middleware.ProxyTargetContextKey, "billing-service")
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
	}

	return func(c *gin.Context) {
		c.Set(middleware.ProxyTargetContextKey, targetURL.Host)
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
