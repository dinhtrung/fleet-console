package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestPrecedenceFlagThenEnvThenFileThenDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "listen: file:1\ndatabase: from-file.db\n")
	env := envMap(map[string]string{"FLEET_CONSOLE_ADDR": "env:2", "FLEET_CONSOLE_DB": "env.db"})
	eff, err := Load(Options{
		FlagPath: path, Flags: map[string]string{"listen": "flag:3"}, Env: env, Home: dir, Cwd: dir,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.Listen != "flag:3" || eff.Sources["listen"] != "flag" {
		t.Fatalf("listen = %s (%s), want the flag", eff.Listen, eff.Sources["listen"])
	}
	if eff.Database != "env.db" || eff.Sources["database"] != "env" {
		t.Fatalf("database = %s (%s), want the env", eff.Database, eff.Sources["database"])
	}
	eff, err = Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.Listen != "file:1" || eff.Sources["listen"] != "file" {
		t.Fatalf("listen = %s (%s), want the file", eff.Listen, eff.Sources["listen"])
	}
	if eff.Database != filepath.Join(dir, "from-file.db") {
		t.Fatalf("database = %s, want it resolved against the config dir", eff.Database)
	}
	eff, err = Load(Options{Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.Listen != defaultListen || eff.Sources["listen"] != "default" {
		t.Fatalf("listen = %s (%s), want the default", eff.Listen, eff.Sources["listen"])
	}
	if eff.TickInterval != defaultTick {
		t.Fatalf("tick = %s, want %s", eff.TickInterval, defaultTick)
	}
}

func TestUnknownKeyNamesIt(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "listen: 127.0.0.1:8787\nnope: true\n")
	_, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error = %v, want the unknown key named", err)
	}
}

func TestMalformedYAMLNamesLine(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "listen: [unclosed\n")
	_, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "line") {
		t.Fatalf("error = %v, want a line number", err)
	}
}

func TestTokenFileMode(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("secret-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, dir, "token_file: token\n")
	_, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir})
	if err == nil || !strings.Contains(err.Error(), "0600") || !strings.Contains(err.Error(), tokenPath) {
		t.Fatalf("error = %v, want the file and 0600", err)
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatal(err)
	}
	eff, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.Token != "secret-token" || eff.Sources["token"] != "token_file" {
		t.Fatalf("token source = %s, want the token_file", eff.Sources["token"])
	}
}

func TestEnvTokenWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "token: inline-token\n")
	eff, err := Load(Options{FlagPath: path, Env: envMap(map[string]string{"FLEET_CONSOLE_TOKEN": "env-token"}), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if eff.Token != "env-token" || eff.Sources["token"] != "env" {
		t.Fatalf("token = %s (%s), want the env token", eff.Token, eff.Sources["token"])
	}
}

func TestInlineTokenWarnsAndPrintRedacts(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "token: super-secret\nlisten: 127.0.0.1:9000\n")
	eff, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(eff.Warnings) == 0 || !strings.Contains(eff.Warnings[0], "token_file") {
		t.Fatalf("warnings = %v, want the inline-token warning", eff.Warnings)
	}
	printed, err := eff.Print()
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	text := string(printed)
	if strings.Contains(text, "super-secret") {
		t.Fatalf("print leaked the token:\n%s", text)
	}
	if !strings.Contains(text, "<redacted>") {
		t.Fatalf("print has no redaction marker:\n%s", text)
	}
	if !strings.Contains(text, "sources:") {
		t.Fatalf("print has no sources block:\n%s", text)
	}
}

func TestProjectValidation(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"duplicate": "projects:\n  - {key: a, forge: github, repo: o/r}\n  - {key: a, forge: github, repo: o/r}\n",
		"forge":     "projects:\n  - {key: a, forge: bitbucket, repo: o/r}\n",
		"repo":      "projects:\n  - {key: a, forge: github}\n",
	}
	for name, body := range cases {
		path := writeConfig(t, dir, body)
		if _, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir}); err == nil {
			t.Fatalf("%s: want an error", name)
		}
		os.Remove(path)
	}
}

func TestMissingMainPathWarns(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "projects:\n  - key: a\n    forge: github\n    repo: o/r\n    rig: a\n    paths:\n      main: ./does-not-exist\n")
	eff, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(eff.Warnings) == 0 || !strings.Contains(eff.Warnings[0], "does not exist") {
		t.Fatalf("warnings = %v", eff.Warnings)
	}
}

func TestLookupOrder(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "fleet-console"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(xdg, "fleet-console"), "listen: xdg:1\n")
	path, err := FindPath(Options{Env: envMap(map[string]string{"XDG_CONFIG_HOME": xdg}), Home: home, Cwd: home})
	if err != nil || path != filepath.Join(xdg, "fleet-console", "config.yaml") {
		t.Fatalf("path = %s, %v", path, err)
	}
	if _, err := FindPath(Options{FlagPath: filepath.Join(home, "missing.yaml"), Home: home, Cwd: home}); err == nil {
		t.Fatal("explicit missing config: want an error")
	}
	path, err = FindPath(Options{Env: envMap(nil), Home: home, Cwd: home})
	if err != nil || path != "" {
		t.Fatalf("no config = %q, %v, want no error", path, err)
	}
}

func TestTickIntervalFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "tick_interval: 10m\n")
	eff, err := Load(Options{FlagPath: path, Home: dir, Cwd: dir})
	if err != nil || eff.TickInterval != 10*time.Minute || eff.Sources["tick_interval"] != "file" {
		t.Fatalf("tick = %s (%s), %v", eff.TickInterval, eff.Sources["tick_interval"], err)
	}
	eff, err = Load(Options{FlagPath: path, Env: envMap(map[string]string{"FLEET_CONSOLE_TICK_INTERVAL": "90s"}), Home: dir, Cwd: dir})
	if err != nil || eff.TickInterval != 90*time.Second || eff.Sources["tick_interval"] != "env" {
		t.Fatalf("tick = %s (%s), %v", eff.TickInterval, eff.Sources["tick_interval"], err)
	}
}

func TestRelativeConfigPathBecomesAbsolute(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(dir, "sub"), "database: app.db\n")
	t.Chdir(dir)
	eff, err := Load(Options{FlagPath: "sub/config.yaml", Env: envMap(nil), Home: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !filepath.IsAbs(eff.ConfigPath) || !filepath.IsAbs(eff.Database) {
		t.Fatalf("config=%q database=%q, want absolute paths", eff.ConfigPath, eff.Database)
	}
	if eff.Database != filepath.Join(dir, "sub", "app.db") {
		t.Fatalf("database = %q", eff.Database)
	}
}

func TestRigsListAndSingleRigForms(t *testing.T) {
	dir := t.TempDir()
	list := writeConfig(t, dir, "projects:\n  - key: a\n    forge: github\n    repo: o/a\n    rigs: [rig-a, rig-b]\n")
	eff, err := Load(Options{FlagPath: list, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(eff.Projects) != 1 || len(eff.Projects[0].Rigs) != 2 || eff.Projects[0].Rigs[0] != "rig-a" || eff.Projects[0].Rigs[1] != "rig-b" {
		t.Fatalf("rigs = %+v", eff.Projects)
	}
	printed, err := eff.Print()
	if err != nil {
		t.Fatalf("print: %v", err)
	}
	if !strings.Contains(string(printed), "rigs:") || !strings.Contains(string(printed), "rig-a") {
		t.Fatalf("print lacks the resolved list:\n%s", printed)
	}

	single := writeConfig(t, dir, "projects:\n  - key: a\n    forge: github\n    repo: o/a\n    rig: rig-a\n")
	eff, err = Load(Options{FlagPath: single, Env: envMap(nil), Home: dir, Cwd: dir})
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	if len(eff.Projects[0].Rigs) != 1 || eff.Projects[0].Rigs[0] != "rig-a" {
		t.Fatalf("single rigs = %+v", eff.Projects[0].Rigs)
	}
	if eff.Projects[0].Rig != "" {
		t.Fatalf("Rig = %q, want the resolved list only", eff.Projects[0].Rig)
	}

	bad := map[string]string{
		"both":      "projects:\n  - {key: a, forge: github, repo: o/a, rig: rig-a, rigs: [rig-b]}\n",
		"empty":     "projects:\n  - {key: a, forge: github, repo: o/a, rigs: []}\n",
		"none":      "projects:\n  - {key: a, forge: github, repo: o/a}\n",
		"duplicate": "projects:\n  - {key: a, forge: github, repo: o/a, rigs: [rig-a, rig-a]}\n",
	}
	for name, body := range bad {
		path := writeConfig(t, dir, body)
		if _, err := Load(Options{FlagPath: path, Env: envMap(nil), Home: dir, Cwd: dir}); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}
