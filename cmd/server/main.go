package main

import (
	"log"

	"github.com/tutorin-id/tutorin-api-gateway/internal/config"
	"github.com/tutorin-id/tutorin-api-gateway/internal/delivery/http"
	"github.com/tutorin-id/tutorin-api-gateway/internal/delivery/http/handler"
)

func main() {
	// Load environment variables
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize proxy handler
	proxyHandler, err := handler.NewProxyHandler(cfg.IdentityServiceURL, cfg.AcademicServiceURL)
	if err != nil {
		log.Fatalf("Failed to initialize proxy handler: %v", err)
	}

	// Setup Router
	r := http.NewRouter(proxyHandler, cfg.JWTSecret)

	log.Printf("Starting API Gateway on port %s...", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start API Gateway: %v", err)
	}
}
