package config_test

import (
	"strings"
	"testing"

	"github.com/harshadptl/gcore/internal/config"
)

func TestProductionRequiresGcoreKey(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SESSION_SECRET", strings.Repeat("x", 32))
	t.Setenv("GOOGLE_CLIENT_ID", "id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "s")
	t.Setenv("GCORE_API_KEY", "")

	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "GCORE_API_KEY") {
		t.Fatalf("want GCORE_API_KEY error, got %v", err)
	}

	t.Setenv("GCORE_API_KEY", "k")
	cfg, err := config.Load()
	if err != nil || cfg.GcoreAPIBase != "https://api.gcore.com" || !cfg.GcoreEnabled() {
		t.Fatalf("%v %+v", err, cfg)
	}
}
