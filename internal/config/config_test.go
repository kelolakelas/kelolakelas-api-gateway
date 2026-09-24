package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*testing.T)
		wantPort string
		wantURL  string
	}{
		{
			name: "environment variables are loaded",
			setup: func(t *testing.T) {
				t.Setenv("PORT", "19000")
				t.Setenv("ACADEMIC_SERVICE_URL", "https://academic.railway.internal")
			},
			wantPort: "19000", wantURL: "https://academic.railway.internal",
		},
		{
			name:     "environment overrides defaults",
			setup:    func(t *testing.T) { t.Setenv("PORT", "49155") },
			wantPort: "49155", wantURL: "http://localhost:8081",
		},
		{
			name:     "missing optional variables use defaults",
			wantPort: "8000", wantURL: "http://localhost:8081",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Chdir(t.TempDir())
			for _, key := range []string{"JWT_SECRET", "APP_URL", "PORT", "IDENTITY_SERVICE_URL", "ACADEMIC_SERVICE_URL", "BILLING_SERVICE_URL", "REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB", "RATE_LIMIT_REQUESTS", "RATE_LIMIT_WINDOW_SECONDS", "RATE_LIMIT_PUBLIC_REQUESTS", "RATE_LIMIT_PROTECTED_REQUESTS", "RATE_LIMIT_LOGIN_REQUESTS", "RATE_LIMIT_REGISTER_REQUESTS", "RATE_LIMIT_WEBHOOK_REQUESTS", "RATE_LIMIT_WEBHOOK_WINDOW_SECONDS"} {
				t.Setenv(key, "")
			}
			t.Setenv("JWT_SECRET", "test-jwt-secret")
			if tt.setup != nil {
				tt.setup(t)
			}

			config, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if config.Port != tt.wantPort || config.AcademicServiceURL != tt.wantURL {
				t.Fatalf("config port=%s academic_url=%s, want port=%s academic_url=%s", config.Port, config.AcademicServiceURL, tt.wantPort, tt.wantURL)
			}
		})
	}
}

func TestRedisConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*testing.T)
		wantTLS      bool
		wantDB       int
		wantUsername string
		wantErr      bool
	}{
		{name: "defaults", wantUsername: "default"},
		{name: "parses TLS and database", setup: func(t *testing.T) {
			t.Setenv("REDIS_TLS", "true")
			t.Setenv("REDIS_DB", "4")
			t.Setenv("REDIS_USERNAME", "upstash")
		}, wantTLS: true, wantDB: 4, wantUsername: "upstash"},
		{name: "rejects invalid database", setup: func(t *testing.T) { t.Setenv("REDIS_DB", "invalid") }, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Chdir(t.TempDir())
			for _, key := range []string{"JWT_SECRET", "REDIS_TLS", "REDIS_DB", "REDIS_USERNAME"} {
				t.Setenv(key, "")
			}
			t.Setenv("JWT_SECRET", "test-jwt-secret")
			if test.setup != nil {
				test.setup(t)
			}
			config, err := LoadConfig()
			if test.wantErr {
				if err == nil {
					t.Fatal("expected configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.RedisTLS != test.wantTLS || config.RedisDB != test.wantDB || config.RedisUsername != test.wantUsername {
				t.Fatalf("redis config=%+v", config)
			}
		})
	}
}

func TestLoadConfigRequiresNonBlankJWTSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{name: "missing secret", wantErr: true},
		{name: "blank secret", secret: " \t ", wantErr: true},
		{name: "valid secret", secret: "test-jwt-secret"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Chdir(t.TempDir())
			t.Setenv("JWT_SECRET", test.secret)

			config, err := LoadConfig()
			if test.wantErr {
				if err == nil {
					t.Fatal("expected JWT_SECRET configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.JWTSecret != test.secret {
				t.Fatalf("JWTSecret=%q, want %q", config.JWTSecret, test.secret)
			}
		})
	}
}

// TestProxyAndServerTimeoutsDefaultToDocumentedValues proves the resilience
// bounds have defaults that hold when nothing is configured, so a deployment
// that predates these variables is still bounded.
func TestProxyAndServerTimeoutsDefaultToDocumentedValues(t *testing.T) {
	viper.Reset()
	t.Chdir(t.TempDir())
	for _, key := range []string{
		"PROXY_UPSTREAM_TIMEOUT_SECONDS", "PROXY_MAX_BODY_BYTES", "SERVER_READ_HEADER_TIMEOUT_SECONDS",
		"SERVER_READ_TIMEOUT_SECONDS", "SERVER_WRITE_TIMEOUT_SECONDS", "SERVER_IDLE_TIMEOUT_SECONDS",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("JWT_SECRET", "test-jwt-secret")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if config.ProxyUpstreamTimeout != DefaultProxyUpstreamTimeout {
		t.Fatalf("ProxyUpstreamTimeout=%d want=%d", config.ProxyUpstreamTimeout, DefaultProxyUpstreamTimeout)
	}
	if config.ProxyMaxBodyBytes != DefaultProxyMaxBodyBytes {
		t.Fatalf("ProxyMaxBodyBytes=%d want=%d", config.ProxyMaxBodyBytes, DefaultProxyMaxBodyBytes)
	}
	if config.ServerReadHeaderTimeout != DefaultServerReadHeaderTimeout ||
		config.ServerReadTimeout != DefaultServerReadTimeout ||
		config.ServerWriteTimeout != DefaultServerWriteTimeout ||
		config.ServerIdleTimeout != DefaultServerIdleTimeout {
		t.Fatalf("server timeouts=%+v", config)
	}
	// The default write timeout must clear the default upstream timeout, or the
	// server would cut a response the proxy is still waiting for.
	if config.ServerWriteTimeout <= config.ProxyUpstreamTimeout {
		t.Fatalf("server write timeout %d does not exceed upstream timeout %d", config.ServerWriteTimeout, config.ProxyUpstreamTimeout)
	}
}

// TestProxyAndServerTimeoutsAreConfigurable proves every bound can be raised or
// lowered from the environment, which the documented variables promise.
func TestProxyAndServerTimeoutsAreConfigurable(t *testing.T) {
	viper.Reset()
	t.Chdir(t.TempDir())
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	t.Setenv("PROXY_UPSTREAM_TIMEOUT_SECONDS", "45")
	t.Setenv("PROXY_MAX_BODY_BYTES", "2097152")
	t.Setenv("SERVER_READ_HEADER_TIMEOUT_SECONDS", "7")
	t.Setenv("SERVER_READ_TIMEOUT_SECONDS", "40")
	t.Setenv("SERVER_WRITE_TIMEOUT_SECONDS", "90")
	t.Setenv("SERVER_IDLE_TIMEOUT_SECONDS", "180")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if config.ProxyUpstreamTimeout != 45 {
		t.Fatalf("ProxyUpstreamTimeout=%d want=45", config.ProxyUpstreamTimeout)
	}
	if config.ProxyMaxBodyBytes != 2097152 {
		t.Fatalf("ProxyMaxBodyBytes=%d want=2097152", config.ProxyMaxBodyBytes)
	}
	if config.ServerReadHeaderTimeout != 7 || config.ServerReadTimeout != 40 ||
		config.ServerWriteTimeout != 90 || config.ServerIdleTimeout != 180 {
		t.Fatalf("server timeouts=%+v", config)
	}
}

// TestServerWriteTimeoutMustExceedUpstreamTimeout proves the cross-field
// constraint fails closed at load time rather than silently clamping a value the
// operator set deliberately.
func TestServerWriteTimeoutMustExceedUpstreamTimeout(t *testing.T) {
	tests := []struct {
		name           string
		upstream       string
		write          string
		wantErr        bool
		wantWriteValue int
	}{
		{name: "write timeout below upstream timeout is rejected", upstream: "60", write: "30", wantErr: true},
		{name: "write timeout equal to upstream timeout is rejected", upstream: "30", write: "30", wantErr: true},
		{name: "write timeout above upstream timeout is accepted", upstream: "30", write: "75", wantWriteValue: 75},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Chdir(t.TempDir())
			t.Setenv("JWT_SECRET", "test-jwt-secret")
			t.Setenv("PROXY_UPSTREAM_TIMEOUT_SECONDS", test.upstream)
			t.Setenv("SERVER_WRITE_TIMEOUT_SECONDS", test.write)

			config, err := LoadConfig()
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected a configuration error for upstream=%s write=%s", test.upstream, test.write)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.ServerWriteTimeout != test.wantWriteValue {
				t.Fatalf("ServerWriteTimeout=%d want=%d", config.ServerWriteTimeout, test.wantWriteValue)
			}
		})
	}
}

// TestProxyMaxBodyBytesAcceptsLargeCallbackPayloads proves the documented limit
// leaves room for the largest current payloads, so the body limit cannot break
// the Duitku webhook or the registration form.
func TestProxyMaxBodyBytesAcceptsLargeCallbackPayloads(t *testing.T) {
	if DefaultProxyMaxBodyBytes < 64*1024 {
		t.Fatalf("default body limit %d bytes is too small for current payloads", DefaultProxyMaxBodyBytes)
	}
}

// KEL-62: without TRUSTED_PROXY_CIDRS and TRUSTED_CLIENT_IP_HEADER no proxy is
// trusted, which keeps the client IP the socket address as before.
func TestClientIPTrustDefaultsToNoProxy(t *testing.T) {
	viper.Reset()
	t.Chdir(t.TempDir())
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("TRUSTED_CLIENT_IP_HEADER", "")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.TrustedProxies) != 0 || config.TrustedClientIPHeader != "" {
		t.Fatalf("default trust proxies=%v header=%q, want none", config.TrustedProxies, config.TrustedClientIPHeader)
	}
}

func TestClientIPTrustIsParsedAndNormalised(t *testing.T) {
	viper.Reset()
	t.Chdir(t.TempDir())
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	t.Setenv("TRUSTED_PROXY_CIDRS", " 10.20.0.0/16, 172.16.5.9 ,,2001:db8:1::/48, ::ffff:192.0.2.1, 10.30.1.7/24")
	t.Setenv("TRUSTED_CLIENT_IP_HEADER", " x-forwarded-for ")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.20.0.0/16", "172.16.5.9/32", "2001:db8:1::/48", "192.0.2.1/32", "10.30.1.0/24"}
	if strings.Join(config.TrustedProxies, ",") != strings.Join(want, ",") {
		t.Fatalf("trusted proxies=%v want %v", config.TrustedProxies, want)
	}
	if config.TrustedClientIPHeader != "X-Forwarded-For" {
		t.Fatalf("header=%q want X-Forwarded-For", config.TrustedClientIPHeader)
	}
}

// AC: an invalid trust configuration stops the gateway at startup with a message
// naming the variable and the offending value.
func TestClientIPTrustRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, cidrs, header, wantMessage string
	}{
		{"malformed range", "10.20.0.0/99", "X-Forwarded-For", `TRUSTED_PROXY_CIDRS entry "10.20.0.0/99" is not a valid IP address or CIDR range`},
		{"hostname", "web.internal", "X-Forwarded-For", `TRUSTED_PROXY_CIDRS entry "web.internal" is not a valid IP address or CIDR range`},
		{"one bad entry among good ones", "10.20.0.0/16, 300.1.1.1", "X-Forwarded-For", `"300.1.1.1" is not a valid`},
		{"zoned address", "fe80::1%eth0", "X-Forwarded-For", `"fe80::1%eth0" is not a valid`},
		{"trust every IPv4 peer", "0.0.0.0/0", "X-Forwarded-For", `"0.0.0.0/0" trusts every address`},
		{"trust every IPv6 peer", "::/0", "X-Forwarded-For", `"::/0" trusts every address`},
		{"header without proxies", "", "X-Forwarded-For", "TRUSTED_CLIENT_IP_HEADER is set but TRUSTED_PROXY_CIDRS is empty"},
		{"proxies without header", "10.20.0.0/16", "", "TRUSTED_PROXY_CIDRS is set but TRUSTED_CLIENT_IP_HEADER is empty"},
		{"invalid header name", "10.20.0.0/16", "X Forwarded For", `TRUSTED_CLIENT_IP_HEADER "X Forwarded For" is not a valid HTTP header name`},
	} {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Chdir(t.TempDir())
			t.Setenv("JWT_SECRET", "test-jwt-secret")
			t.Setenv("TRUSTED_PROXY_CIDRS", test.cidrs)
			t.Setenv("TRUSTED_CLIENT_IP_HEADER", test.header)

			_, err := LoadConfig()
			if err == nil {
				t.Fatalf("configuration cidrs=%q header=%q was accepted", test.cidrs, test.header)
			}
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("error %q does not contain %q", err, test.wantMessage)
			}
		})
	}
}
