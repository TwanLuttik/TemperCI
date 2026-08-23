package control

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseTailscaleStatusJSON(t *testing.T) {
	raw := `{
	  "BackendState": "Running",
	  "Self": {
	    "DNSName": "pve.tail123.ts.net.",
	    "TailscaleIPs": ["100.77.4.36"]
	  }
	}`
	st, err := parseTailscaleStatusJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if st.DNSName != "pve.tail123.ts.net" {
		t.Fatalf("dns=%q", st.DNSName)
	}
	if st.IP != "100.77.4.36" {
		t.Fatalf("ip=%q", st.IP)
	}
}

func TestFunnelHostnameFromStatus(t *testing.T) {
	got := funnelHostnameFromStatus("https://pve.tail123.ts.net (Funnel on)\n|-- / proxy http://127.0.0.1:8080\n")
	if got != "pve.tail123.ts.net" {
		t.Fatalf("got %q", got)
	}
	if funnelHostnameFromStatus("No serve config") != "" {
		t.Fatal("expected empty when funnel off")
	}
}

func TestDetectWebhookEndpoints_TailscaleFunnel(t *testing.T) {
	eps := detectWebhookEndpoints(webhookDetectOpts{
		ListenAddr: "0.0.0.0:8080",
		Run: func(name string, args ...string) (string, error) {
			if name == "tailscale" && len(args) > 0 && args[0] == "status" {
				return `{"BackendState":"Running","Self":{"DNSName":"pve.tail123.ts.net.","TailscaleIPs":["100.77.4.36"]}}`, nil
			}
			if name == "tailscale" && len(args) > 0 && args[0] == "funnel" {
				return "https://pve.tail123.ts.net\n|-- / proxy http://127.0.0.1:8080\n", nil
			}
			return "", errors.New("missing")
		},
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
	})
	ep := findEndpoint(eps, "tailscale_funnel")
	if ep == nil || ep.URL != "https://pve.tail123.ts.net/webhooks/github" || !ep.Public {
		t.Fatalf("funnel = %+v endpoints=%+v", ep, eps)
	}
}

func TestDetectWebhookEndpoints_CloudflaredConfig(t *testing.T) {
	eps := detectWebhookEndpoints(webhookDetectOpts{
		ListenAddr: "0.0.0.0:8080",
		Run:        func(string, ...string) (string, error) { return "", errors.New("missing") },
		ReadFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "config.yml") {
				return []byte("tunnel: abc\ningress:\n  - hostname: ci.example.com\n    service: http://127.0.0.1:8080\n"), nil
			}
			return nil, os.ErrNotExist
		},
	})
	ep := findEndpoint(eps, "cloudflare")
	if ep == nil || ep.URL != "https://ci.example.com/webhooks/github" || !ep.Public {
		t.Fatalf("cloudflare = %+v", ep)
	}
}

func TestWebhookURLFromRequestHost(t *testing.T) {
	ep := webhookURLFromRequestHost("pve.tail123.ts.net:8080", "0.0.0.0:8080")
	if ep == nil || !strings.Contains(ep.URL, "/webhooks/github") || !strings.Contains(ep.URL, "pve.tail123.ts.net") {
		t.Fatalf("got %+v", ep)
	}
	if webhookURLFromRequestHost("127.0.0.1:8080", "0.0.0.0:8080") != nil {
		t.Fatal("loopback must not be suggested as a webhook URL")
	}
}

func findEndpoint(eps []webhookEndpoint, kind string) *webhookEndpoint {
	for i := range eps {
		if eps[i].Kind == kind {
			return &eps[i]
		}
	}
	return nil
}
