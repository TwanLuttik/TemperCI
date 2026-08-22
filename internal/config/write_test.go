package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPathBeside(t *testing.T) {
	got := AgentPathBeside("/etc/temperci/control.toml")
	if got != "/etc/temperci/agent.toml" {
		t.Fatalf("AgentPathBeside = %q", got)
	}
	if AgentPathBeside("") != "/etc/temperci/agent.toml" {
		t.Fatalf("empty control path should default to /etc/temperci/agent.toml")
	}
}

func TestPatchAgentTOMLString_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	orig := "agent_token = \"tok\"\ncache_listen_addr = \"127.0.0.1:8743\"\nmin_ready = 1\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PatchAgentTOMLString(path, "cache_listen_addr", ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `cache_listen_addr = ""`) {
		t.Fatalf("expected empty cache_listen_addr, got:\n%s", text)
	}
	if !strings.Contains(text, `agent_token = "tok"`) || !strings.Contains(text, "min_ready = 1") {
		t.Fatalf("lost other keys:\n%s", text)
	}
	if strings.Contains(text, "127.0.0.1:8743") {
		t.Fatalf("old listen addr still present:\n%s", text)
	}
}

func TestPatchAgentTOMLString_AppendsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(path, []byte("agent_token = \"tok\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PatchAgentTOMLString(path, "cache_listen_addr", "127.0.0.1:8743"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `cache_listen_addr = "127.0.0.1:8743"`) {
		t.Fatalf("missing appended key:\n%s", raw)
	}
}

func TestReadAgentTOMLString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(path, []byte("cache_listen_addr = \"127.0.0.1:8743\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadAgentTOMLString(path, "cache_listen_addr")
	if !ok || got != "127.0.0.1:8743" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := ReadAgentTOMLString(filepath.Join(dir, "missing.toml"), "cache_listen_addr"); ok {
		t.Fatal("expected missing file")
	}
}

func TestEnsureAgentTOML_WritesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	created, err := EnsureAgentTOML(path, "http://127.0.0.1:8080", "tok", dir)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `agent_token = "tok"`) {
		t.Fatalf("missing token:\n%s", raw)
	}
	created, err = EnsureAgentTOML(path, "http://127.0.0.1:8080", "other", dir)
	if err != nil || created {
		t.Fatalf("second write created=%v err=%v", created, err)
	}
	raw2, _ := os.ReadFile(path)
	if strings.Contains(string(raw2), "other") {
		t.Fatal("existing file was overwritten")
	}
}

func TestEnsureAgentTOML_RequiresToken(t *testing.T) {
	_, err := EnsureAgentTOML(filepath.Join(t.TempDir(), "agent.toml"), "", "", "")
	if err == nil {
		t.Fatal("expected token error")
	}
}
