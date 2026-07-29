package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	JWTSecret          string `mapstructure:"JWT_SECRET"`
	Port               string `mapstructure:"PORT"`
	IdentityServiceURL string `mapstructure:"IDENTITY_SERVICE_URL"`
	AcademicServiceURL string `mapstructure:"ACADEMIC_SERVICE_URL"`
}

func LoadConfig() (Config, error) {
	// Load using godotenv just to make sure OS environment is populated,
	// though Viper's AutomaticEnv also works.
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found by godotenv")
	}

	viper.SetConfigFile(".env")
	if err := viper.ReadInConfig(); err != nil {
		if !os.IsNotExist(err) {
			return Config{}, err
		}
		log.Println("No .env file found by Viper, using system environment variables")
	}

	viper.AutomaticEnv()

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
	if config.IdentityServiceURL == "" {
		config.IdentityServiceURL = "http://localhost:8080"
	}
	if config.AcademicServiceURL == "" {
		config.AcademicServiceURL = "http://localhost:8081"
	}

	return config, nil
}
