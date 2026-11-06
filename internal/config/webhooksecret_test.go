package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The secret resolves from a 0600 file exactly as token_file does.
func TestWebhookSecretResolvesFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook-secret")
	if err := os.WriteFile(path, []byte("  shared-secret-value\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	write(t, cfg, `
token: "board-token"
webhook_secret_file: "`+path+`"
projects:
  - key: app
    forge: github
    repo: owner/app
    rigs: [primary]
`)
	eff, err := Load(Options{FlagPath: cfg, Home: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if eff.WebhookSecret != "shared-secret-value" {
		t.Fatalf("WebhookSecret = %q, want the trimmed file content", eff.WebhookSecret)
	}
	if got := eff.Sources["webhook_secret_file"]; got != "file" {
		t.Fatalf("source = %q, want file", got)
	}
}

// A secret file readable by others is refused rather than used: a config mistake
// must not silently weaken a credential.
func TestWebhookSecretRefusesLooseMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook-secret")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	write(t, cfg, `
token: "board-token"
webhook_secret_file: "`+path+`"
projects:
  - key: app
    forge: github
    repo: owner/app
    rigs: [primary]
`)
	_, err := Load(Options{FlagPath: cfg, Home: dir})
	if err == nil {
		t.Fatal("Load accepted a 0644 secret file, want a refusal naming the mode")
	}
	if !strings.Contains(err.Error(), "0600") {
		t.Fatalf("error = %q, want it to name the required mode", err.Error())
	}
}

// Unset means the receiver is off, and the source says so — an upgraded box gains no
// public route until the operator asks for one.
func TestWebhookSecretAbsentLeavesReceiverOff(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	write(t, cfg, `
token: "board-token"
projects:
  - key: app
    forge: github
    repo: owner/app
    rigs: [primary]
`)
	eff, err := Load(Options{FlagPath: cfg, Home: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if eff.WebhookSecret != "" {
		t.Fatalf("WebhookSecret = %q, want empty", eff.WebhookSecret)
	}
	if got := eff.Sources["webhook_secret_file"]; got != "none" {
		t.Fatalf("source = %q, want none", got)
	}
}

// The secret never appears in the printed view, but its key and source do.
func TestWebhookSecretRedactedInPrint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hook-secret")
	const secret = "must-never-appear-in-output"
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	write(t, cfg, `
token: "board-token"
webhook_secret_file: "`+path+`"
projects:
  - key: app
    forge: github
    repo: owner/app
    rigs: [primary]
`)
	eff, err := Load(Options{FlagPath: cfg, Home: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := eff.Print()
	if err != nil {
		t.Fatalf("Print: %v", err)
	}
	printed := string(out)
	if strings.Contains(printed, secret) {
		t.Fatalf("printed view leaks the secret:\n%s", printed)
	}
	if !strings.Contains(printed, "webhook_secret: <redacted>") {
		t.Fatalf("printed view does not name the redacted secret:\n%s", printed)
	}
	if !strings.Contains(printed, "webhook_secret_file: "+path) {
		t.Fatalf("printed view does not name the secret's source:\n%s", printed)
	}
}

// write drops a config file into a temp dir.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
