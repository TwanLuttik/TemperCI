package control

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

type setupCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (s *Server) setupSnapshot(requestHost string) map[string]any {
	cfg := &config.ControlConfig{}
	if s.dash != nil && s.dash.Config != nil {
		cfg = s.dash.Config
	}
	dbDone := false
	adminUsers := 0
	if s.dash != nil && s.dash.Store != nil {
		dbDone, _ = s.dash.Store.SetupCompleted()
		adminUsers, _ = s.dash.Store.UserCount()
	}
	pemPath := strings.TrimSpace(cfg.GitHubAppPrivateKeyPath)
	if pemPath == "" {
		pemPath = "/etc/temperci/github-app.pem"
	}
	pemSet := pemLooksKey(pemPath)
	webhookSet := strings.TrimSpace(cfg.GitHubWebhookSecret) != ""
	tokenSet := strings.TrimSpace(cfg.AgentToken) != ""
	orgSet := strings.TrimSpace(cfg.GitHubOrg) != ""
	appSet := cfg.GitHubAppID != 0
	cacheAddr, cacheOK := "", false
	imageOK, kernelOK := false, false
	if s.dash != nil {
		cacheAddr, cacheOK = config.ReadAgentTOMLString(s.dash.agentConfigPath(), "cache_listen_addr")
		img, _ := config.ReadAgentTOMLString(s.dash.agentConfigPath(), "image_path")
		kern, _ := config.ReadAgentTOMLString(s.dash.agentConfigPath(), "kernel_path")
		if img == "" {
			img = filepath.Join(cfg.DataDir, "images", "ubuntu-2404-runner.ext4")
		}
		if kern == "" {
			kern = filepath.Join(cfg.DataDir, "images", "vmlinux")
		}
		imageOK = pathExists(img)
		kernelOK = pathExists(kern)
	}
	agents := 0
	if s.agents != nil {
		agents = len(s.agents.List())
	}
	hostctl := s.hostctlAvailable()

	accessStatus, accessDetail := "missing", "choose open or password auth"
	if strings.EqualFold(cfg.AuthMode, "open") {
		accessStatus, accessDetail = "ok", "open (no login)"
	} else if strings.EqualFold(cfg.AuthMode, "password") {
		if adminUsers > 0 {
			accessStatus = "ok"
			accessDetail = "password · " + strconv.Itoa(adminUsers) + " user(s)"
		} else {
			accessStatus = "warn"
			accessDetail = "password mode with no admin user yet"
		}
	}

	ghStatus, ghDetail := "missing", "App ID, org, webhook secret, and PEM required"
	switch {
	case appSet && orgSet && webhookSet && pemSet:
		ghStatus, ghDetail = "ok", cfg.GitHubOrg+" · app "+strconv.FormatInt(cfg.GitHubAppID, 10)
	case appSet || orgSet || webhookSet || pemSet:
		ghStatus = "warn"
		var missing []string
		if !appSet {
			missing = append(missing, "app id")
		}
		if !orgSet {
			missing = append(missing, "org")
		}
		if !webhookSet {
			missing = append(missing, "webhook secret")
		}
		if !pemSet {
			missing = append(missing, "private key")
		}
		ghDetail = "partial · missing " + strings.Join(missing, ", ")
	}

	agentStatus, agentDetail := "missing", "shared agent token not set"
	if tokenSet {
		agentStatus = "ok"
		agentDetail = "token set · listen " + cfg.ListenAddr
		if cacheOK && cacheAddr != "" {
			agentDetail += " · cache " + cacheAddr
		}
	}

	svcStatus, svcDetail := "missing", "no agent registered"
	switch {
	case agents > 0 && hostctl:
		svcStatus = "ok"
		svcDetail = strconv.Itoa(agents) + " agent(s) registered · hostctl available"
	case agents > 0:
		svcStatus, svcDetail = "ok", strconv.Itoa(agents)+" agent(s) registered"
	case hostctl:
		svcStatus, svcDetail = "warn", "hostctl installed · waiting for agent"
	}
	if imageOK && kernelOK {
		svcDetail += " · guest image ready"
	} else if !imageOK || !kernelOK {
		if svcStatus == "ok" {
			svcStatus = "warn"
		}
		svcDetail += " · guest image or kernel missing"
	}

	needs := cfg.NeedsSetup() && !dbDone
	wh := s.webhookSnapshot(requestHost, cfg.ListenAddr)
	received, _ := wh["received"].(bool)
	lastEvent, _ := wh["last_event"].(string)
	suggested, _ := wh["suggested_url"].(string)
	return map[string]any{
		"ok":              true,
		"needs_setup":     needs,
		"setup_completed": cfg.SetupCompleted || dbDone,
		"auth_mode":       cfg.AuthMode,
		"fleet_ready":     s.dash != nil && s.dash.FleetReady && !cfg.NeedsSetup(),
		"org":             cfg.GitHubOrg,
		"listen_addr":     cfg.ListenAddr,
		"webhook":         wh,
		"values": map[string]any{
			"auth_mode":              cfg.AuthMode,
			"github_org":             cfg.GitHubOrg,
			"github_app_id":          cfg.GitHubAppID,
			"listen_addr":            cfg.ListenAddr,
			"cache_listen_addr":      cacheAddr,
			"webhook_set":            webhookSet,
			"webhook_received":       received,
			"webhook_last_event":     lastEvent,
			"webhook_url":            suggested,
			"pem_set":                pemSet,
			"agent_token_set":        tokenSet,
			"admin_users":            adminUsers,
			"agents_registered":      agents,
			"hostctl":                hostctl,
			"guest_image":            imageOK,
			"guest_kernel":           kernelOK,
			"github_app_private_key": pemPath,
		},
		"steps": []setupCheck{
			{ID: "access", Label: "Access", Status: accessStatus, Detail: accessDetail},
			{ID: "github", Label: "GitHub App", Status: ghStatus, Detail: ghDetail},
			{ID: "agent", Label: "Agent", Status: agentStatus, Detail: agentDetail},
			{ID: "services", Label: "Services", Status: svcStatus, Detail: svcDetail},
		},
	}
}

func pemLooksKey(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), "PRIVATE KEY")
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
