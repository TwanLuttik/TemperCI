package ocicache

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func defaultAnonymousToken(host, name string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	if h != "registry-1.docker.io" {
		return "", nil
	}
	if name == "" {
		return "", nil
	}
	q := url.Values{}
	q.Set("service", "registry.docker.io")
	q.Set("scope", "repository:"+name+":pull")
	req, err := http.NewRequest(http.MethodGet, "https://auth.docker.io/token?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ocicache: hub token %s", resp.Status)
	}
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Token != "" {
		return out.Token, nil
	}
	return out.AccessToken, nil
}
