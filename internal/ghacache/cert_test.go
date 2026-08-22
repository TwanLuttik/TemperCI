package ghacache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateCA_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a1, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.key")); err != nil {
		t.Fatal(err)
	}
	a2, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Cert.SerialNumber.Cmp(a2.Cert.SerialNumber) != 0 {
		t.Fatal("reloaded CA does not match")
	}
	if _, err := a2.Certificate("results-receiver.actions.githubusercontent.com"); err != nil {
		t.Fatal(err)
	}
}
