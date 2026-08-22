package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAgentSrc_PrefersExplicit(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "temperci-agent")
	if err := os.WriteFile(src, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveAgentSrc(src, filepath.Join(dir, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Fatalf("got %q", got)
	}
}

func TestInstallFile_CopiesAndSamePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dest := filepath.Join(dir, "dest")
	if err := os.WriteFile(src, []byte("agent-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installFile(src, dest, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "agent-bytes" {
		t.Fatalf("dest=%q", got)
	}
	if err := installFile(dest, dest, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAgentSrc_Missing(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveAgentSrc("", filepath.Join(dir, "nope"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err=%v", err)
	}
}
