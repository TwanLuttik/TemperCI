package control

import "strings"

// DefaultLabelPrefix is the default TemperCI runs-on label prefix.
const DefaultLabelPrefix = "temperci-"

// OwnedLabels returns labels that TemperCI should handle.
// A job is owned when at least one label starts with prefix (case-sensitive).
// Returns only the matching labels (for JIT registration).
func OwnedLabels(labels []string, prefix string) []string {
	if prefix == "" {
		prefix = DefaultLabelPrefix
	}
	var out []string
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// IsOwned reports whether any job label is owned by TemperCI.
func IsOwned(labels []string, prefix string) bool {
	return len(OwnedLabels(labels, prefix)) > 0
}
