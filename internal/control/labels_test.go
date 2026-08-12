package control

import (
	"reflect"
	"testing"
)

func TestOwnedLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		prefix string
		want   []string
	}{
		{
			name:   "temperci only",
			labels: []string{"temperci-4vcpu-ubuntu-2404"},
			want:   []string{"temperci-4vcpu-ubuntu-2404"},
		},
		{
			name:   "mixed keeps only owned",
			labels: []string{"self-hosted", "temperci-2vcpu-ubuntu-2404", "linux"},
			want:   []string{"temperci-2vcpu-ubuntu-2404"},
		},
		{
			name:   "github hosted ignored",
			labels: []string{"ubuntu-latest"},
			want:   nil,
		},
		{
			name:   "custom prefix",
			labels: []string{"tc-4vcpu", "other"},
			prefix: "tc-",
			want:   []string{"tc-4vcpu"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OwnedLabels(tt.labels, tt.prefix)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("OwnedLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}
