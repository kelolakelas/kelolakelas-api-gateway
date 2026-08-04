package config

import (
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
