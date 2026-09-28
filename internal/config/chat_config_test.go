package config

import (
	"testing"

	"github.com/spf13/viper"
)

// TestLoadConfigChatServiceURLOptional proves CHAT_SERVICE_URL is optional and
// empty by default (KEL-122): an operator that never sets it gets the 503 chat
// behaviour, not a silent localhost default.
func TestLoadConfigChatServiceURLOptional(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*testing.T)
		wantURL string
	}{
		{name: "empty by default", wantURL: ""},
		{
			name:    "reads the environment",
			setup:   func(t *testing.T) { t.Setenv("CHAT_SERVICE_URL", "http://chat.internal:8083") },
			wantURL: "http://chat.internal:8083",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			viper.Reset()
			t.Chdir(t.TempDir())
			t.Setenv("JWT_SECRET", "test-jwt-secret")
			t.Setenv("CHAT_SERVICE_URL", "")
			if test.setup != nil {
				test.setup(t)
			}
			config, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if config.ChatServiceURL != test.wantURL {
				t.Fatalf("chat url=%q want=%q", config.ChatServiceURL, test.wantURL)
			}
		})
	}
}
