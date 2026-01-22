package agent

import (
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Use t.Setenv with empty values to ensure clean state
	// t.Setenv automatically restores original values after test
	envVars := []string{
		"TRIX_DATABASE_PATH",
		"TRIX_POLL_INTERVAL",
		"TRIX_NAMESPACES",
		"TRIX_CLUSTER_NAME",
		"TRIX_SAAS_ENDPOINT",
		"TRIX_SAAS_API_KEY",
		"TRIX_LOG_FORMAT",
		"TRIX_LOG_LEVEL",
		"TRIX_HEALTH_ADDR",
	}
	for _, v := range envVars {
		t.Setenv(v, "")
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"DatabasePath", cfg.DatabasePath, "/data/trix.db"},
		{"PollInterval", cfg.PollInterval, 5 * time.Minute},
		{"LogFormat", cfg.LogFormat, "json"},
		{"LogLevel", cfg.LogLevel, "info"},
		{"HealthAddr", cfg.HealthAddr, ":8080"},
		{"Namespaces", len(cfg.Namespaces), 0},
		{"ClusterName", cfg.ClusterName, ""},
		{"SaasEndpoint", cfg.SaasEndpoint, ""},
		{"SaasApiKey", cfg.SaasApiKey, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestLoadConfig_WithEnvVars(t *testing.T) {
	// t.Setenv automatically cleans up after test
	t.Setenv("TRIX_DATABASE_PATH", "/custom/path/trix.db")
	t.Setenv("TRIX_POLL_INTERVAL", "10m")
	t.Setenv("TRIX_NAMESPACES", "default, kube-system, monitoring")
	t.Setenv("TRIX_CLUSTER_NAME", "production-cluster")
	t.Setenv("TRIX_SAAS_ENDPOINT", "https://app.trixsec.dev")
	t.Setenv("TRIX_SAAS_API_KEY", "secret-api-key")
	t.Setenv("TRIX_LOG_FORMAT", "text")
	t.Setenv("TRIX_LOG_LEVEL", "debug")
	t.Setenv("TRIX_HEALTH_ADDR", ":9090")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	if cfg.DatabasePath != "/custom/path/trix.db" {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath, "/custom/path/trix.db")
	}
	if cfg.PollInterval != 10*time.Minute {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 10*time.Minute)
	}
	if cfg.ClusterName != "production-cluster" {
		t.Errorf("ClusterName = %q, want %q", cfg.ClusterName, "production-cluster")
	}
	if cfg.SaasEndpoint != "https://app.trixsec.dev" {
		t.Errorf("SaasEndpoint = %q, want %q", cfg.SaasEndpoint, "https://app.trixsec.dev")
	}
	if cfg.SaasApiKey != "secret-api-key" {
		t.Errorf("SaasApiKey = %q, want %q", cfg.SaasApiKey, "secret-api-key")
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want %q", cfg.LogFormat, "text")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.HealthAddr != ":9090" {
		t.Errorf("HealthAddr = %q, want %q", cfg.HealthAddr, ":9090")
	}

	// Verify namespaces are parsed and trimmed correctly
	expectedNS := []string{"default", "kube-system", "monitoring"}
	if len(cfg.Namespaces) != len(expectedNS) {
		t.Errorf("Namespaces length = %d, want %d", len(cfg.Namespaces), len(expectedNS))
	}
	for i, ns := range expectedNS {
		if i < len(cfg.Namespaces) && cfg.Namespaces[i] != ns {
			t.Errorf("Namespaces[%d] = %q, want %q", i, cfg.Namespaces[i], ns)
		}
	}
}

func TestLoadConfig_InvalidPollInterval(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"invalid format", "notaduration"},
		{"missing unit", "10"},
		{"empty number", "m"},
		{"negative without unit", "-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TRIX_POLL_INTERVAL", tt.value)

			_, err := LoadConfig()
			if err == nil {
				t.Errorf("LoadConfig() with TRIX_POLL_INTERVAL=%q should return error", tt.value)
			}
		})
	}
}

func TestConfig_HasSaasEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		saasEndpoint string
		saasApiKey   string
		want         bool
	}{
		{
			name:         "both endpoint and key present",
			saasEndpoint: "https://app.trixsec.dev",
			saasApiKey:   "secret-key",
			want:         true,
		},
		{
			name:         "only endpoint present",
			saasEndpoint: "https://app.trixsec.dev",
			saasApiKey:   "",
			want:         false,
		},
		{
			name:         "only key present",
			saasEndpoint: "",
			saasApiKey:   "secret-key",
			want:         false,
		},
		{
			name:         "neither present",
			saasEndpoint: "",
			saasApiKey:   "",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				SaasEndpoint: tt.saasEndpoint,
				SaasApiKey:   tt.saasApiKey,
			}
			if got := cfg.HasSaasEndpoint(); got != tt.want {
				t.Errorf("HasSaasEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}
