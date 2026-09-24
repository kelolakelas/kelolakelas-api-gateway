package config

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Defaults for the proxy and server timeouts. They are exported as a single
// block so the documentation and the environment example quote one source.
const (
	// DefaultProxyUpstreamTimeout bounds one proxied request end to end. It is
	// generous enough for the slowest legitimate downstream call observed today
	// (tenant location geocoding) while still releasing a caller when a
	// downstream service stops responding.
	DefaultProxyUpstreamTimeout = 30
	// DefaultProxyMaxBodyBytes bounds a request body. The largest bodies the
	// platform accepts are the Duitku callback payload and the registration and
	// invitation forms, all of which are a few kilobytes, so 1 MiB leaves room
	// to grow without letting a caller stream an unbounded body through the
	// gateway. It matches middleware.DefaultMaxBodyBytes.
	DefaultProxyMaxBodyBytes int64 = 1 << 20
	// DefaultServerReadHeaderTimeout bounds how long a client may take to send
	// request headers, which is the slowloris window a public gateway must close.
	DefaultServerReadHeaderTimeout = 5
	// DefaultServerReadTimeout bounds reading the request including its body.
	DefaultServerReadTimeout = 30
	// DefaultServerWriteTimeout must exceed the upstream timeout, otherwise the
	// server would cut a response the proxy was still waiting for.
	DefaultServerWriteTimeout = 60
	// DefaultServerIdleTimeout bounds how long an idle keep-alive connection is
	// reused.
	DefaultServerIdleTimeout = 120
)

type Config struct {
	JWTSecret          string `mapstructure:"JWT_SECRET"`
	APPURL             string `mapstructure:"APP_URL"`
	Port               string `mapstructure:"PORT"`
	IdentityServiceURL string `mapstructure:"IDENTITY_SERVICE_URL"`
	AcademicServiceURL string `mapstructure:"ACADEMIC_SERVICE_URL"`
	BillingServiceURL  string `mapstructure:"BILLING_SERVICE_URL"`
	RedisHost          string `mapstructure:"REDIS_HOST"`
	RedisPort          string `mapstructure:"REDIS_PORT"`
	RedisUsername      string `mapstructure:"REDIS_USERNAME"`
	RedisPassword      string `mapstructure:"REDIS_PASSWORD"`
	RedisTLS           bool   `mapstructure:"REDIS_TLS"`
	RedisDB            int    `mapstructure:"REDIS_DB"`
	RateLimitRequests  int    `mapstructure:"RATE_LIMIT_REQUESTS"`
	RateLimitWindow    int    `mapstructure:"RATE_LIMIT_WINDOW_SECONDS"`
	RateLimitPublic    int    `mapstructure:"RATE_LIMIT_PUBLIC_REQUESTS"`
	RateLimitProtected int    `mapstructure:"RATE_LIMIT_PROTECTED_REQUESTS"`
	RateLimitLogin     int    `mapstructure:"RATE_LIMIT_LOGIN_REQUESTS"`
	RateLimitRegister  int    `mapstructure:"RATE_LIMIT_REGISTER_REQUESTS"`
	RateLimitWebhook   int    `mapstructure:"RATE_LIMIT_WEBHOOK_REQUESTS"`
	WebhookWindow      int    `mapstructure:"RATE_LIMIT_WEBHOOK_WINDOW_SECONDS"`

	// ProxyUpstreamTimeout bounds a single proxied request in seconds.
	ProxyUpstreamTimeout int `mapstructure:"PROXY_UPSTREAM_TIMEOUT_SECONDS"`
	// ProxyMaxBodyBytes bounds an accepted request body in bytes.
	ProxyMaxBodyBytes int64 `mapstructure:"PROXY_MAX_BODY_BYTES"`
	// ServerReadHeaderTimeout, ServerReadTimeout, ServerWriteTimeout and
	// ServerIdleTimeout configure the HTTP server the gateway runs in seconds.
	ServerReadHeaderTimeout int `mapstructure:"SERVER_READ_HEADER_TIMEOUT_SECONDS"`
	ServerReadTimeout       int `mapstructure:"SERVER_READ_TIMEOUT_SECONDS"`
	ServerWriteTimeout      int `mapstructure:"SERVER_WRITE_TIMEOUT_SECONDS"`
	ServerIdleTimeout       int `mapstructure:"SERVER_IDLE_TIMEOUT_SECONDS"`

	// TrustedProxyCIDRsRaw is the comma-separated TRUSTED_PROXY_CIDRS value as
	// read from the environment; TrustedProxies holds its validated entries.
	// Only a request whose socket peer is inside one of these ranges may name
	// the client IP through TrustedClientIPHeader. Both are empty by default,
	// which trusts no proxy: the client IP is always the socket address.
	TrustedProxyCIDRsRaw  string   `mapstructure:"TRUSTED_PROXY_CIDRS"`
	TrustedProxies        []string `mapstructure:"-"`
	TrustedClientIPHeader string   `mapstructure:"TRUSTED_CLIENT_IP_HEADER"`
}

func LoadConfig() (Config, error) {
	// Load using godotenv just to make sure OS environment is populated,
	// though Viper's AutomaticEnv also works.
	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found by godotenv")
	}

	viper.SetConfigFile(".env")
	if err := viper.ReadInConfig(); err != nil {
		if !os.IsNotExist(err) {
			return Config{}, err
		}
		slog.Info("No .env file found by Viper, using system environment variables")
	}

	viper.AutomaticEnv()
	for _, key := range []string{
		"JWT_SECRET", "APP_URL", "PORT", "IDENTITY_SERVICE_URL", "ACADEMIC_SERVICE_URL", "BILLING_SERVICE_URL",
		"REDIS_HOST", "REDIS_PORT", "REDIS_USERNAME", "REDIS_PASSWORD", "REDIS_TLS", "REDIS_DB", "RATE_LIMIT_REQUESTS",
		"RATE_LIMIT_WINDOW_SECONDS", "RATE_LIMIT_PUBLIC_REQUESTS", "RATE_LIMIT_PROTECTED_REQUESTS",
		"RATE_LIMIT_LOGIN_REQUESTS", "RATE_LIMIT_REGISTER_REQUESTS", "RATE_LIMIT_WEBHOOK_REQUESTS",
		"RATE_LIMIT_WEBHOOK_WINDOW_SECONDS",
		"PROXY_UPSTREAM_TIMEOUT_SECONDS", "PROXY_MAX_BODY_BYTES", "SERVER_READ_HEADER_TIMEOUT_SECONDS",
		"SERVER_READ_TIMEOUT_SECONDS", "SERVER_WRITE_TIMEOUT_SECONDS", "SERVER_IDLE_TIMEOUT_SECONDS",
		"TRUSTED_PROXY_CIDRS", "TRUSTED_CLIENT_IP_HEADER",
	} {
		if err := viper.BindEnv(key); err != nil {
			return Config{}, err
		}
	}

	var parsedRedisTLS bool
	if redisTLS := viper.GetString("REDIS_TLS"); redisTLS != "" {
		var err error
		parsedRedisTLS, err = strconv.ParseBool(redisTLS)
		if err != nil {
			return Config{}, fmt.Errorf("REDIS_TLS must be a boolean: %w", err)
		}
	}
	parsedRedisDB := 0
	if redisDB := viper.GetString("REDIS_DB"); redisDB != "" {
		var err error
		parsedRedisDB, err = strconv.Atoi(redisDB)
		if err != nil || parsedRedisDB < 0 {
			return Config{}, fmt.Errorf("REDIS_DB must be a non-negative integer")
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return Config{}, err
	}
	config.RedisTLS = parsedRedisTLS
	config.RedisDB = parsedRedisDB

	// JWT verification must never fall back to a source-defined secret.
	if strings.TrimSpace(config.JWTSecret) == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if config.Port == "" {
		config.Port = "8000"
	}
	if config.APPURL != "" {
		parsedURL, err := url.Parse(config.APPURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || (parsedURL.Path != "" && parsedURL.Path != "/") || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || parsedURL.User != nil {
			return Config{}, fmt.Errorf("APP_URL must be a valid origin without a path")
		}
		config.APPURL = strings.TrimRight(parsedURL.Scheme+"://"+parsedURL.Host, "/")
	}
	if config.IdentityServiceURL == "" {
		config.IdentityServiceURL = "http://localhost:8080"
	}
	if config.AcademicServiceURL == "" {
		config.AcademicServiceURL = "http://localhost:8081"
	}
	if config.BillingServiceURL == "" {
		config.BillingServiceURL = "http://localhost:8082"
	}
	if config.RedisHost == "" {
		config.RedisHost = "localhost"
	}
	if config.RedisPort == "" {
		config.RedisPort = "6379"
	}
	if config.RedisUsername == "" {
		config.RedisUsername = "default"
	}
	if config.RateLimitRequests <= 0 {
		config.RateLimitRequests = 60
	}
	if config.RateLimitWindow <= 0 {
		config.RateLimitWindow = 60
	}
	if config.RateLimitPublic <= 0 {
		config.RateLimitPublic = config.RateLimitRequests
	}
	if config.RateLimitProtected <= 0 {
		config.RateLimitProtected = 120
	}
	if config.RateLimitLogin <= 0 {
		config.RateLimitLogin = 5
	}
	if config.RateLimitRegister <= 0 {
		config.RateLimitRegister = 10
	}
	if config.RateLimitWebhook <= 0 {
		config.RateLimitWebhook = 120
	}
	if config.WebhookWindow <= 0 {
		config.WebhookWindow = config.RateLimitWindow
	}
	if config.ProxyUpstreamTimeout <= 0 {
		config.ProxyUpstreamTimeout = DefaultProxyUpstreamTimeout
	}
	if config.ProxyMaxBodyBytes <= 0 {
		config.ProxyMaxBodyBytes = DefaultProxyMaxBodyBytes
	}
	if config.ServerReadHeaderTimeout <= 0 {
		config.ServerReadHeaderTimeout = DefaultServerReadHeaderTimeout
	}
	if config.ServerReadTimeout <= 0 {
		config.ServerReadTimeout = DefaultServerReadTimeout
	}
	if config.ServerWriteTimeout <= 0 {
		config.ServerWriteTimeout = DefaultServerWriteTimeout
	}
	if config.ServerIdleTimeout <= 0 {
		config.ServerIdleTimeout = DefaultServerIdleTimeout
	}

	// A write timeout shorter than the upstream timeout would cut a response the
	// proxy is still waiting for, turning a slow downstream into a transport
	// error instead of the 504 the proxy is meant to return.
	if config.ServerWriteTimeout <= config.ProxyUpstreamTimeout {
		return Config{}, fmt.Errorf(
			"SERVER_WRITE_TIMEOUT_SECONDS (%d) must be greater than PROXY_UPSTREAM_TIMEOUT_SECONDS (%d)",
			config.ServerWriteTimeout, config.ProxyUpstreamTimeout)
	}

	trustedProxies, clientIPHeader, err := parseClientIPTrust(config.TrustedProxyCIDRsRaw, config.TrustedClientIPHeader)
	if err != nil {
		return Config{}, err
	}
	config.TrustedProxies = trustedProxies
	config.TrustedClientIPHeader = clientIPHeader

	return config, nil
}

// headerNamePattern is the RFC 9110 token alphabet for a field name.
var headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// parseClientIPTrust validates the trusted-proxy policy that decides where the
// gateway reads the client IP from (KEL-62).
//
// The client IP keys the rate limiter, so a policy that trusts too much lets a
// caller rotate a forged header value and escape the login limit. The rules are
// therefore strict and fail at startup rather than at request time:
//   - both variables empty: no proxy is trusted, which is the previous behaviour;
//   - both must be set together, because a header without trusted proxies would
//     be silently ignored and trusted proxies without a header would be useless;
//   - every entry must be an IP address or a CIDR range, and a zero-length
//     prefix (0.0.0.0/0, ::/0) is rejected because it trusts every peer.
func parseClientIPTrust(rawCIDRs, rawHeader string) ([]string, string, error) {
	header := strings.TrimSpace(rawHeader)
	var proxies []string
	for _, entry := range strings.Split(rawCIDRs, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		prefix, err := parseProxyEntry(entry)
		if err != nil {
			return nil, "", fmt.Errorf("TRUSTED_PROXY_CIDRS entry %q is not a valid IP address or CIDR range", entry)
		}
		if prefix.Bits() == 0 {
			return nil, "", fmt.Errorf("TRUSTED_PROXY_CIDRS entry %q trusts every address; list the proxy ranges explicitly", entry)
		}
		proxies = append(proxies, prefix.String())
	}

	switch {
	case len(proxies) == 0 && header == "":
		return nil, "", nil
	case len(proxies) == 0:
		return nil, "", fmt.Errorf("TRUSTED_CLIENT_IP_HEADER is set but TRUSTED_PROXY_CIDRS is empty; set both or neither")
	case header == "":
		return nil, "", fmt.Errorf("TRUSTED_PROXY_CIDRS is set but TRUSTED_CLIENT_IP_HEADER is empty; set both or neither")
	case !headerNamePattern.MatchString(header):
		return nil, "", fmt.Errorf("TRUSTED_CLIENT_IP_HEADER %q is not a valid HTTP header name", header)
	}
	return proxies, http.CanonicalHeaderKey(header), nil
}

// parseProxyEntry accepts a CIDR range or a single address, which is read as a
// host-length prefix. Host bits in a range are masked away.
func parseProxyEntry(entry string) (netip.Prefix, error) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, err
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, err
	}
	if addr.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("zoned address")
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
