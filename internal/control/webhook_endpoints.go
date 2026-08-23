package control

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const webhookPath = "/webhooks/github"

type webhookEndpoint struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	URL    string `json:"url"`
	Public bool   `json:"public"`
	Detail string `json:"detail"`
}

type webhookDetectOpts struct {
	ListenAddr string
	Run        func(name string, args ...string) (string, error)
	ReadFile   func(path string) ([]byte, error)
}

type tailscaleStatus struct {
	DNSName string
	IP      string
}

var (
	funnelHTTPSRe   = regexp.MustCompile(`https://([A-Za-z0-9.-]+\.ts\.net)`)
	cfHostnameRe    = regexp.MustCompile(`(?m)^\s*-?\s*hostname:\s*['"]?([A-Za-z0-9.-]+\.[A-Za-z0-9.-]+)['"]?`)
	probeWebhookTTL = 20 * time.Second
)

var webhookProbe = struct {
	mu        sync.Mutex
	at        time.Time
	endpoints []webhookEndpoint
}{}

func parseTailscaleStatusJSON(raw string) (tailscaleStatus, error) {
	var parsed struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return tailscaleStatus{}, err
	}
	st := tailscaleStatus{
		DNSName: strings.TrimSuffix(strings.TrimSpace(parsed.Self.DNSName), "."),
	}
	for _, ip := range parsed.Self.TailscaleIPs {
		if strings.Contains(ip, ":") {
			continue
		}
		st.IP = ip
		break
	}
	if st.IP == "" && len(parsed.Self.TailscaleIPs) > 0 {
		st.IP = parsed.Self.TailscaleIPs[0]
	}
	return st, nil
}

func funnelHostnameFromStatus(raw string) string {
	m := funnelHTTPSRe.FindStringSubmatch(raw)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func listenPort(addr string) string {
	_, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || port == "" {
		return "8080"
	}
	return port
}

func webhookURL(scheme, host, port string, implicitHTTPS bool) string {
	host = strings.TrimSpace(host)
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return ""
	}
	if implicitHTTPS || port == "" || port == "443" || port == "80" {
		return scheme + "://" + host + webhookPath
	}
	return scheme + "://" + host + ":" + port + webhookPath
}

func cloudflaredHostname(raw string) string {
	m := cfHostnameRe.FindStringSubmatch(raw)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func webhookURLFromRequestHost(host, listenAddr string) *webhookEndpoint {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p = host, listenPort(listenAddr)
	}
	ip := net.ParseIP(h)
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	public := ip == nil && !isPrivateHost(h)
	scheme := "http"
	if public || strings.HasSuffix(h, ".ts.net") {
		// Prefer https for named hosts; GitHub requires https for public webhooks.
		if public {
			scheme = "https"
			p = ""
		}
	}
	url := webhookURL(scheme, h, p, scheme == "https")
	if url == "" {
		return nil
	}
	detail := "URL you used to open this dashboard"
	if !public {
		detail = "GitHub cannot reach a private/LAN address — use Funnel, Cloudflare Tunnel, or a public host"
	}
	return &webhookEndpoint{
		Kind:   "dashboard",
		Label:  "This dashboard URL",
		URL:    url,
		Public: public,
		Detail: detail,
	}
}

func isPrivateHost(h string) bool {
	ip := net.ParseIP(h)
	if ip == nil {
		return strings.HasSuffix(h, ".local")
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func detectWebhookEndpoints(opts webhookDetectOpts) []webhookEndpoint {
	run := opts.Run
	if run == nil {
		run = func(name string, args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...)
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
	}
	read := opts.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	port := listenPort(opts.ListenAddr)
	var out []webhookEndpoint

	if raw, err := run("tailscale", "status", "--json"); err == nil {
		if st, err := parseTailscaleStatusJSON(raw); err == nil && st.DNSName != "" {
			funnelHost := ""
			if fs, err := run("tailscale", "funnel", "status"); err == nil {
				funnelHost = funnelHostnameFromStatus(fs)
			}
			if funnelHost != "" {
				out = append(out, webhookEndpoint{
					Kind:   "tailscale_funnel",
					Label:  "Tailscale Funnel",
					URL:    webhookURL("https", funnelHost, "", true),
					Public: true,
					Detail: "GitHub can reach this Funnel hostname",
				})
			} else {
				out = append(out, webhookEndpoint{
					Kind:   "tailscale",
					Label:  "Tailscale",
					URL:    webhookURL("https", st.DNSName, port, false),
					Public: false,
					Detail: "MagicDNS is tailnet-only. Enable Funnel (or Cloudflare Tunnel) so GitHub can deliver webhooks",
				})
			}
		}
	}

	for _, path := range []string{"/etc/cloudflared/config.yml", "/etc/cloudflared/config.yaml"} {
		raw, err := read(path)
		if err != nil {
			continue
		}
		host := cloudflaredHostname(string(raw))
		if host == "" {
			continue
		}
		out = append(out, webhookEndpoint{
			Kind:   "cloudflare",
			Label:  "Cloudflare Tunnel",
			URL:    webhookURL("https", host, "", true),
			Public: true,
			Detail: "Hostname from " + path,
		})
		break
	}

	return out
}

func cachedWebhookEndpoints(listenAddr string) []webhookEndpoint {
	webhookProbe.mu.Lock()
	defer webhookProbe.mu.Unlock()
	if time.Since(webhookProbe.at) < probeWebhookTTL && webhookProbe.endpoints != nil {
		return webhookProbe.endpoints
	}
	eps := detectWebhookEndpoints(webhookDetectOpts{ListenAddr: listenAddr})
	webhookProbe.at = time.Now()
	webhookProbe.endpoints = eps
	return eps
}

func mergeWebhookEndpoints(extra *webhookEndpoint, eps []webhookEndpoint) []webhookEndpoint {
	if extra == nil || extra.URL == "" {
		return eps
	}
	for _, e := range eps {
		if e.URL == extra.URL {
			return eps
		}
	}
	return append([]webhookEndpoint{*extra}, eps...)
}

func (s *Server) webhookSnapshot(requestHost, listenAddr string) map[string]any {
	if listenAddr == "" {
		listenAddr = "0.0.0.0:8080"
	}
	eps := cachedWebhookEndpoints(listenAddr)
	eps = mergeWebhookEndpoints(webhookURLFromRequestHost(requestHost, listenAddr), eps)
	out := map[string]any{
		"received":  false,
		"endpoints": eps,
	}
	if s.dash != nil && s.dash.Store != nil {
		if last, err := s.dash.Store.LastWebhookDelivery(); err == nil && last != nil {
			out["received"] = true
			out["last_at"] = last.At.UTC().Format(time.RFC3339)
			out["last_event"] = last.Event
			if last.Delivery != "" {
				out["last_delivery"] = last.Delivery
			}
		}
	}
	// A minted/assigned job is proof GitHub delivered workflow_job. Operators
	// cannot easily redeliver the original ping once it is buried in App history.
	if rec, _ := out["received"].(bool); !rec && s.store != nil && s.store.Len() > 0 {
		out["received"] = true
		out["last_event"] = "workflow_job"
		if recent := s.store.ListRecent(1); len(recent) > 0 && !recent[0].CreatedAt.IsZero() {
			out["last_at"] = recent[0].CreatedAt.UTC().Format(time.RFC3339)
		}
	}
	if sug := pickSuggestedWebhook(eps); sug != nil {
		out["suggested_url"] = sug.URL
		out["suggested_kind"] = sug.Kind
		out["suggested_public"] = sug.Public
		out["suggested_detail"] = sug.Detail
	}
	return out
}

func pickSuggestedWebhook(eps []webhookEndpoint) *webhookEndpoint {
	for i := range eps {
		if eps[i].Public {
			return &eps[i]
		}
	}
	if len(eps) > 0 {
		return &eps[0]
	}
	return nil
}
