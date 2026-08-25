package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

const defaultImageToken = "ubuntu-2404"

// ExclusiveJobMiB is the guest RAM at which a job must run alone on the host.
// A 12g e2e packed with a 6g API test OOM'd a 31 GiB box (job 97648498475).
const ExclusiveJobMiB = 12 * 1024

// ExclusiveShape reports whether this guest size must not share the host.
func ExclusiveShape(memoryMiB int) bool {
	return memoryMiB >= ExclusiveJobMiB
}

var shapeLabelRe = regexp.MustCompile(`(?i)^temperci-(\d+)vcpu(?:-(\d+)g)?(?:-([a-z0-9.-]+))?$`)

// VMShape is one guest size the pool can warm or cold-boot.
type VMShape struct {
	Label     string
	VCPUs     int
	MemoryMiB int
	MinReady  int
}

func (s VMShape) key() shapeKey {
	return shapeKey{VCPUs: s.VCPUs, MemoryMiB: s.MemoryMiB}
}

type shapeKey struct {
	VCPUs     int
	MemoryMiB int
}

// ShapeLabel builds the runs-on label for a size.
// 4 vCPU / 8192 MiB stays temperci-4vcpu-ubuntu-2404 so existing workflows match.
func ShapeLabel(vcpus, memoryMiB int) string {
	if vcpus <= 0 {
		vcpus = 2
	}
	if memoryMiB <= 0 {
		memoryMiB = 2048
	}
	if vcpus == 4 && memoryMiB == 8192 {
		return "temperci-4vcpu-" + defaultImageToken
	}
	g := memoryMiB / 1024
	if g < 1 {
		g = 1
	}
	return fmt.Sprintf("temperci-%dvcpu-%dg-%s", vcpus, g, defaultImageToken)
}

// ParseShapeLabel reads vCPU and optional RAM (GiB) from a TemperCI runs-on label.
// When the label omits RAM, memGiB is 0.
func ParseShapeLabel(label string) (vcpus, memGiB int, ok bool) {
	m := shapeLabelRe.FindStringSubmatch(strings.TrimSpace(label))
	if m == nil {
		return 0, 0, false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil || v <= 0 {
		return 0, 0, false
	}
	if m[2] != "" {
		g, err := strconv.Atoi(m[2])
		if err != nil || g <= 0 {
			return 0, 0, false
		}
		return v, g, true
	}
	return v, 0, true
}

// ResolveJobShape picks the guest size for a job from its labels.
// A parsed label wins even when that size is not in the warm catalog (cold-boot).
// Missing RAM uses a catalog shape with the same vCPU, else defaultMemMiB.
func ResolveJobShape(labels []string, catalog []VMShape, defaultVCPU, defaultMemMiB int) VMShape {
	def := defaultShape(catalog, defaultVCPU, defaultMemMiB)
	for _, raw := range labels {
		vcpus, memGiB, ok := ParseShapeLabel(raw)
		if !ok {
			continue
		}
		mem := memGiB * 1024
		if memGiB == 0 {
			if s, found := findShapeByVCPU(catalog, vcpus); found {
				mem = s.MemoryMiB
			} else {
				mem = def.MemoryMiB
			}
		}
		if s, found := findShape(catalog, vcpus, mem); found {
			return s
		}
		return VMShape{Label: ShapeLabel(vcpus, mem), VCPUs: vcpus, MemoryMiB: mem}
	}
	return def
}

func defaultShape(catalog []VMShape, defaultVCPU, defaultMemMiB int) VMShape {
	if len(catalog) > 0 {
		return catalog[0]
	}
	if defaultVCPU <= 0 {
		defaultVCPU = 2
	}
	if defaultMemMiB <= 0 {
		defaultMemMiB = 2048
	}
	return VMShape{
		Label:     ShapeLabel(defaultVCPU, defaultMemMiB),
		VCPUs:     defaultVCPU,
		MemoryMiB: defaultMemMiB,
	}
}

func findShape(catalog []VMShape, vcpus, mem int) (VMShape, bool) {
	for _, s := range catalog {
		if s.VCPUs == vcpus && s.MemoryMiB == mem {
			return s, true
		}
	}
	return VMShape{}, false
}

func findShapeByVCPU(catalog []VMShape, vcpus int) (VMShape, bool) {
	for _, s := range catalog {
		if s.VCPUs == vcpus {
			return s, true
		}
	}
	return VMShape{}, false
}

// ShapesFromConfig maps agent.toml shapes (or the legacy single size) into pool shapes.
func ShapesFromConfig(cfg *config.AgentConfig) []VMShape {
	if cfg == nil {
		return nil
	}
	out := make([]VMShape, 0, len(cfg.Shapes)+1)
	for _, s := range cfg.EffectiveShapes() {
		out = append(out, VMShape{
			Label:     s.Label,
			VCPUs:     s.VCPU,
			MemoryMiB: s.MemoryMiB,
			MinReady:  s.MinReady,
		})
	}
	return out
}
