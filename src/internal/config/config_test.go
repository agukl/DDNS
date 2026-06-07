package config

import (
	"os"
	"path/filepath"
	"testing"

	"ddns/internal/provider"
)

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{
	  "records": [
	    {
	      "provider": "Cloudflare",
	      "zone": "example.com",
	      "name": "home.example.com"
	    }
	  ],
	  "providers": {
	    "Cloudflare": {
	      "api_token_env": "CLOUDFLARE_API_TOKEN"
	    }
	  }
	}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IntervalSeconds != 300 {
		t.Fatalf("IntervalSeconds = %d, want 300", cfg.IntervalSeconds)
	}
	if cfg.IPProbe.Type != "ipv4" {
		t.Fatalf("IPProbe.Type = %q, want ipv4", cfg.IPProbe.Type)
	}
	if cfg.Records[0].Provider != "cloudflare" {
		t.Fatalf("provider = %q, want cloudflare", cfg.Records[0].Provider)
	}
	if cfg.Records[0].Type != "A" {
		t.Fatalf("type = %q, want A", cfg.Records[0].Type)
	}
	if got := cfg.ResolvePath(cfg.StateFile); got != filepath.Join(dir, ".ddns-state.json") {
		t.Fatalf("ResolvePath(StateFile) = %q", got)
	}
	if err := cfg.ValidateRecords(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRecordsRejectsUnsupportedType(t *testing.T) {
	cfg := Config{
		Records: []provider.TargetRecord{
			{
				Provider: "cloudflare",
				Zone:     "example.com",
				Name:     "home.example.com",
				Type:     "AAAA",
			},
		},
	}
	if err := cfg.ValidateRecords(); err == nil {
		t.Fatal("ValidateRecords succeeded, want unsupported type error")
	}
}
