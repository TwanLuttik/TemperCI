package ghacache

import "testing"

func TestShouldIntercept(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"results-receiver.actions.githubusercontent.com", true},
		{"tempercicache.blob.core.windows.net", true},
		// Real Actions artifact/cache Azure accounts stay spliced.
		{"productionresultssa0.blob.core.windows.net", false},
		{"api.github.com", false},
		{"github.com", false},
		{"objects.githubusercontent.com", false},
		{"RESULTS-RECEIVER.ACTIONS.GITHUBUSERCONTENT.COM", true},
	}
	for _, tc := range cases {
		if got := ShouldIntercept(tc.host); got != tc.want {
			t.Fatalf("ShouldIntercept(%q)=%v want %v", tc.host, got, tc.want)
		}
	}
}

func TestListenPort(t *testing.T) {
	if p := ListenPort("127.0.0.1:8743"); p != 8743 {
		t.Fatalf("port=%d", p)
	}
	if p := ListenPort("off"); p != 0 {
		t.Fatalf("off port=%d", p)
	}
}
