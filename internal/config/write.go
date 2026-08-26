package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// AgentPathBeside returns the sibling agent.toml next to a control.toml path.
func AgentPathBeside(controlPath string) string {
	if strings.TrimSpace(controlPath) == "" {
		return "/etc/temperci/agent.toml"
	}
	return filepath.Join(filepath.Dir(controlPath), "agent.toml")
}

// ReadAgentTOMLString reads a top-level string key from an agent TOML file.
// ok is false when the file cannot be read or parsed.
func ReadAgentTOMLString(path, key string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var m map[string]any
	if err := toml.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	v, exists := m[key]
	if !exists || v == nil {
		return "", true
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprint(v), true
	}
	return strings.TrimSpace(s), true
}

// PatchAgentTOMLString sets a top-level string key in an agent TOML file,
// replacing an existing assignment or appending one. Other keys are left intact.
func PatchAgentTOMLString(path, key, value string) error {
	if path == "" {
		return fmt.Errorf("config: empty path")
	}
	if key == "" || strings.ContainsAny(key, " \t\n=") {
		return fmt.Errorf("config: invalid toml key %q", key)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	line := fmt.Sprintf("%s = %q", key, value)
	re, err := regexp.Compile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*.*$`)
	if err != nil {
		return err
	}
	text := string(raw)
	if re.MatchString(text) {
		text = re.ReplaceAllString(text, line)
	} else {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += line + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// EnsureAgentTOML writes a collocated agent.toml if path does not exist.
// Existing files are left untouched. Returns true when a new file was created.
func EnsureAgentTOML(path, controlURL, token, dataDir string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, fmt.Errorf("config: empty path")
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("config: stat %s: %w", path, err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, fmt.Errorf("config: agent_token is required to write %s", path)
	}
	if strings.TrimSpace(controlURL) == "" {
		controlURL = "http://127.0.0.1:8080"
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "/var/lib/temperci"
	}
	body := fmt.Sprintf(`# Written by the TemperCI dashboard (first-time agent install).
control_url = %q
agent_token = %q
data_dir = %q
image_path = %q
kernel_path = %q
vmm_backend = "firecracker"
min_ready = 1
max_ready = 2
job_simulate_seconds = 0
cache_listen_addr = ""
`, controlURL, token, dataDir,
		filepath.Join(dataDir, "images", "ubuntu-2404-runner.ext4"),
		filepath.Join(dataDir, "images", "vmlinux"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, err
	}
	return true, nil
}

// WriteAgentShapes replaces [[shapes]] tables and syncs top-level vcpu/memory_mib/min_ready
// from the first shape so older fields stay consistent.
func WriteAgentShapes(path string, shapes []VMShapeConfig) error {
	if path == "" {
		return fmt.Errorf("config: empty path")
	}
	for i := range shapes {
		if err := normalizeShape(&shapes[i], 4, 8192); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	text := stripTOMLTables(string(raw), "shapes")
	if len(shapes) == 0 {
		// No catalog: keep existing vcpu/memory_mib defaults, drop warm target.
		text = upsertTOMLInt(text, "min_ready", 0)
	} else {
		text = upsertTOMLInt(text, "vcpu", shapes[0].VCPU)
		text = upsertTOMLInt(text, "memory_mib", shapes[0].MemoryMiB)
		text = upsertTOMLInt(text, "min_ready", shapes[0].MinReady)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if len(shapes) > 0 {
		text += "\n"
		for _, s := range shapes {
			text += "[[shapes]]\n"
			text += fmt.Sprintf("label = %q\n", s.Label)
			text += fmt.Sprintf("vcpu = %d\n", s.VCPU)
			text += fmt.Sprintf("memory_mib = %d\n", s.MemoryMiB)
			text += fmt.Sprintf("min_ready = %d\n\n", s.MinReady)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func stripTOMLTables(text, name string) string {
	re := regexp.MustCompile(`(?m)^\[\[` + regexp.QuoteMeta(name) + `\]\]\s*\n(?:^[^\[][^\n]*\n)*`)
	return strings.TrimRight(re.ReplaceAllString(text, ""), "\n") + "\n"
}

func upsertTOMLInt(text, key string, value int) string {
	line := fmt.Sprintf("%s = %d", key, value)
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*.*$`)
	if re.MatchString(text) {
		return re.ReplaceAllString(text, line)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + line + "\n"
}

// WriteControlFile writes cfg as TOML to path (mode 0600).
func WriteControlFile(path string, cfg *ControlConfig) error {
	if path == "" {
		return fmt.Errorf("config: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
