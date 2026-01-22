package agent

import (
	"os"
	"testing"
	"time"
)

// clearEnvVars removes all TRIX_ environment variables to ensure clean test state.
func clearEnvVars(t *testing.T) {
	t.Helper()
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
		os.Unsetenv(v)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	clearEnvVars(t)

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
	clearEnvVars(t)
	defer clearEnvVars(t)

	// Set all environment variables
	os.Setenv("TRIX_DATABASE_PATH", "/custom/path/trix.db")
	os.Setenv("TRIX_POLL_INTERVAL", "10m")
	os.Setenv("TRIX_NAMESPACES", "default, kube-system, monitoring")
	os.Setenv("TRIX_CLUSTER_NAME", "production-cluster")
	os.Setenv("TRIX_SAAS_ENDPOINT", "https://app.trixsec.dev")
	os.Setenv("TRIX_SAAS_API_KEY", "secret-api-key")
	os.Setenv("TRIX_LOG_FORMAT", "text")
	os.Setenv("TRIX_LOG_LEVEL", "debug")
	os.Setenv("TRIX_HEALTH_ADDR", ":9090")

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
	clearEnvVars(t)
	defer clearEnvVars(t)

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
			clearEnvVars(t)
			os.Setenv("TRIX_POLL_INTERVAL", tt.value)

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
