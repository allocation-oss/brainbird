package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestLoadUsesPlatformPort(t *testing.T) {
	t.Setenv("PORT", "18765")
	t.Setenv("HTTP_ADDR", "")
	t.Chdir(t.TempDir())
	viper.Reset()
	t.Cleanup(viper.Reset)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTPAddr != ":18765" {
		t.Fatalf("expected HTTP address to use platform port, got %q", cfg.HTTPAddr)
	}
}
