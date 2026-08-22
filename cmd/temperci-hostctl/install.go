package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	defaultAgentBinary = "/usr/local/bin/temperci-agent"
	defaultAgentUnit   = "/etc/systemd/system/temperci-agent.service"
)

const agentUnitContents = `[Unit]
Description=TemperCI host agent
Documentation=https://github.com/TwanLuttik/TemperCI
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
ExecStart=/usr/local/bin/temperci-agent -config /etc/temperci/agent.toml
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

func installAgent(src, destBin, unitPath string) error {
	resolved, err := resolveAgentSrc(src, destBin)
	if err != nil {
		return err
	}
	if err := installFile(resolved, destBin, 0o755); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("unit dir: %w", err)
	}
	if err := os.WriteFile(unitPath, []byte(agentUnitContents), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if err := exec.Command("systemctl", "enable", "--now", "temperci-agent.service").Run(); err != nil {
		return fmt.Errorf("systemctl enable --now temperci-agent: %w", err)
	}
	fmt.Printf("installed %s and enabled temperci-agent.service\n", destBin)
	return nil
}

func resolveAgentSrc(src, destBin string) (string, error) {
	candidates := []string{}
	if strings.TrimSpace(src) != "" {
		candidates = append(candidates, src)
	}
	candidates = append(candidates, destBin)
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "temperci-agent"))
	}
	if p, err := exec.LookPath("temperci-agent"); err == nil {
		candidates = append(candidates, p)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("temperci-agent binary not found (place it at %s or next to temperci-hostctl)", destBin)
}

func installFile(src, dest string, mode os.FileMode) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		srcAbs = src
	}
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		destAbs = dest
	}
	if srcAbs == destAbs {
		return os.Chmod(dest, mode)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
