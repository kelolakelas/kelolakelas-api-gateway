package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestLoadConfigNormalizesAppURL(t *testing.T) {
	viper.Reset()
	t.Setenv("APP_URL", "https://frontend.example.com/")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.APPURL != "https://frontend.example.com" {
		t.Fatalf("APPURL=%q", config.APPURL)
	}
}

func TestLoadConfigRejectsAppURLPath(t *testing.T) {
	viper.Reset()
	t.Setenv("APP_URL", "https://frontend.example.com/app")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected APP_URL validation error")
	}
}
