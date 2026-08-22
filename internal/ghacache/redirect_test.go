package ghacache

import (
	"strings"
	"testing"
)

func TestSplitListenAddr(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
	}{
		{"127.0.0.1:8743", "127.0.0.1", 8743},
		{"0.0.0.0:8743", "0.0.0.0", 8743},
		{":8743", "", 8743},
		{"[::1]:8743", "::1", 8743},
		{"off", "", 0},
		{"", "", 0},
	}
	for _, tc := range cases {
		h, p := SplitListenAddr(tc.in)
		if h != tc.host || p != tc.port {
			t.Errorf("SplitListenAddr(%q)=(%q,%d) want (%q,%d)", tc.in, h, p, tc.host, tc.port)
		}
	}
}

func TestGuestHTTPSRedirectSpec_LoopbackUsesDNAT(t *testing.T) {
	spec, loop := GuestHTTPSRedirectSpec("tc00aabbcc", "127.0.0.1:8743")
	if !loop {
		t.Fatal("expected loopback DNAT")
	}
	got := strings.Join(spec, " ")
	if !strings.Contains(got, "-j DNAT") || !strings.Contains(got, "127.0.0.1:8743") {
		t.Fatalf("spec=%q", got)
	}
	if !strings.Contains(got, "-i tc00aabbcc") || !strings.Contains(got, "--dport 443") {
		t.Fatalf("spec=%q", got)
	}
}

func TestGuestHTTPSRedirectSpec_WildcardUsesREDIRECT(t *testing.T) {
	spec, loop := GuestHTTPSRedirectSpec("tc00aabbcc", "0.0.0.0:8743")
	if loop {
		t.Fatal("wildcard should not enable route_localnet")
	}
	got := strings.Join(spec, " ")
	if !strings.Contains(got, "-j REDIRECT") || !strings.Contains(got, "--to-ports 8743") {
		t.Fatalf("spec=%q", got)
	}
}

func TestGuestHTTPSRedirectSpec_Empty(t *testing.T) {
	if spec, _ := GuestHTTPSRedirectSpec("", "127.0.0.1:8743"); spec != nil {
		t.Fatalf("empty tap spec=%v", spec)
	}
	if spec, _ := GuestHTTPSRedirectSpec("tap0", ""); spec != nil {
		t.Fatalf("empty addr spec=%v", spec)
	}
}

func TestGuestHTTPSInputSpec_AcceptsTapToListenPort(t *testing.T) {
	spec := GuestHTTPSInputSpec("tcb2579bda", "127.0.0.1:8743")
	got := strings.Join(spec, " ")
	// Must match INPUT (not FORWARD): PVEFW-HOST-IN drops NEW tcp/8743
	// arriving on the tap after DNAT. Insert-at-top is done by RedirectGuestHTTPS.
	if !strings.Contains(got, "INPUT") || !strings.Contains(got, "-i tcb2579bda") {
		t.Fatalf("spec=%q", got)
	}
	if !strings.Contains(got, "--dport 8743") || !strings.Contains(got, "-j ACCEPT") {
		t.Fatalf("spec=%q", got)
	}
}

func TestGuestHTTPSInputSpec_Empty(t *testing.T) {
	if spec := GuestHTTPSInputSpec("", "127.0.0.1:8743"); spec != nil {
		t.Fatalf("empty tap spec=%v", spec)
	}
	if spec := GuestHTTPSInputSpec("tap0", ""); spec != nil {
		t.Fatalf("empty addr spec=%v", spec)
	}
}
