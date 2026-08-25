package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadYAMLConfig(t *testing.T) {
	path := writeConfig(t, `
server:
  listen_address: "127.0.0.1:9000"
database:
  dsn: "postgres://example"
  max_connections: 12
account_id: "personal"
auth:
  macbook: "secret-one"
  imac: "secret-two"
`)

	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Server.ListenAddress != "127.0.0.1:9000" || config.Database.DSN != "postgres://example" {
		t.Fatalf("unexpected config: %#v", config)
	}
	if config.Database.MaxConnections != 12 || config.AccountID != "personal" {
		t.Fatalf("unexpected config defaults: %#v", config)
	}
	if config.Auth["macbook"] != "secret-one" || config.Auth["imac"] != "secret-two" {
		t.Fatalf("unexpected auth map: %#v", config.Auth)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
database:
  dsn: "postgres://example"
auth:
  macbook: "secret-one"
`)

	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Server.ListenAddress != ":8080" || config.Database.MaxConnections != 20 || config.AccountID != "default" {
		t.Fatalf("unexpected defaults: %#v", config)
	}
}

func TestLoadRejectsDuplicateSecrets(t *testing.T) {
	path := writeConfig(t, `
database:
  dsn: "postgres://example"
auth:
  macbook: "same-secret"
  imac: "same-secret"
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "same secret") {
		t.Fatalf("expected duplicate secret error, got %v", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, `
database:
  dsn: "postgres://example"
auth:
  macbook: "secret-one"
unknown: true
`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
