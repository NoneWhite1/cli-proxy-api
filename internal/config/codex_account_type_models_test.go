package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfigOptional_CodexAccountTypeModels(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
codex:
  account-type-models:
    Plus:
      - gpt-5.6-sol
    free: []
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	plusModels, ok := cfg.Codex.ModelsForAccountType("plus")
	if !ok {
		t.Fatal("plus account type rule not found")
	}
	if want := []string{"gpt-5.6-sol"}; !reflect.DeepEqual(plusModels, want) {
		t.Fatalf("plus models = %#v, want %#v", plusModels, want)
	}

	freeModels, ok := cfg.Codex.ModelsForAccountType("FREE")
	if !ok {
		t.Fatal("free account type rule not found")
	}
	if len(freeModels) != 0 {
		t.Fatalf("free models = %#v, want an empty allowlist", freeModels)
	}
	if _, ok := cfg.Codex.ModelsForAccountType("pro"); ok {
		t.Fatal("unexpected pro account type rule")
	}
}
