package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
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
	RedisPassword      string `mapstructure:"REDIS_PASSWORD"`
	RedisDB            int    `mapstructure:"REDIS_DB"`
	RateLimitRequests  int    `mapstructure:"RATE_LIMIT_REQUESTS"`
	RateLimitWindow    int    `mapstructure:"RATE_LIMIT_WINDOW_SECONDS"`
	RateLimitPublic    int    `mapstructure:"RATE_LIMIT_PUBLIC_REQUESTS"`
	RateLimitProtected int    `mapstructure:"RATE_LIMIT_PROTECTED_REQUESTS"`
	RateLimitLogin     int    `mapstructure:"RATE_LIMIT_LOGIN_REQUESTS"`
	RateLimitRegister  int    `mapstructure:"RATE_LIMIT_REGISTER_REQUESTS"`
	RateLimitWebhook   int    `mapstructure:"RATE_LIMIT_WEBHOOK_REQUESTS"`
	WebhookWindow      int    `mapstructure:"RATE_LIMIT_WEBHOOK_WINDOW_SECONDS"`
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
		"REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB", "RATE_LIMIT_REQUESTS",
		"RATE_LIMIT_WINDOW_SECONDS", "RATE_LIMIT_PUBLIC_REQUESTS", "RATE_LIMIT_PROTECTED_REQUESTS",
		"RATE_LIMIT_LOGIN_REQUESTS", "RATE_LIMIT_REGISTER_REQUESTS", "RATE_LIMIT_WEBHOOK_REQUESTS",
		"RATE_LIMIT_WEBHOOK_WINDOW_SECONDS",
	} {
		if err := viper.BindEnv(key); err != nil {
			return Config{}, err
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return Config{}, err
	}

	// Default fallback values
	if config.JWTSecret == "" {
		config.JWTSecret = "supersecretjwtkey123!"
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

	return config, nil
}
