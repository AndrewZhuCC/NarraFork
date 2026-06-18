package chapters

import (
	"reflect"
	"testing"
)

// TestContainerPortsFromRawSupersetOfTS documents and locks containerPortsFromRaw
// / collectContainerPorts as an intentional SUPERSET of the TypeScript
// extractContainerPortsFromComposePs (server/services/container-service.ts).
//
// TS reads only container_port || TargetPort from a flat array under
// `container.Ports ?? container.Publishers` (one or the other). Go additionally:
//   - merges BOTH the Ports and Publishers keys,
//   - also reads ContainerPort and target keys,
//   - parses docker-style "host->container/proto" mapping strings,
//   - recurses into nested arrays.
//
// This is a deliberate defensive widening to tolerate podman/compose version
// variance; for the real podman `compose ps --format json` payload that emits
// Publishers[].TargetPort, Go and TS agree. The result is always sorted+deduped
// and fed as ONE hint into mergePortHints alongside configured + inspected
// ports, so the superset only ever adds (never corrupts) port hints. This test
// pins that contract so future audits treat it as examined, not an open gap.
func TestContainerPortsFromRawSupersetOfTS(t *testing.T) {
	// Shared with TS: Publishers[].TargetPort (the real podman compose ps shape).
	tsShared := map[string]any{
		"Publishers": []any{
			map[string]any{"PublishedPort": float64(18080), "TargetPort": float64(8080)},
		},
	}
	if got := containerPortsFromRaw(tsShared); !reflect.DeepEqual(got, []int{8080}) {
		t.Fatalf("Publishers TargetPort = %v, want [8080]", got)
	}

	// Shared with TS: Ports[].container_port.
	tsContainerPort := map[string]any{
		"Ports": []any{map[string]any{"container_port": float64(3000)}},
	}
	if got := containerPortsFromRaw(tsContainerPort); !reflect.DeepEqual(got, []int{3000}) {
		t.Fatalf("Ports container_port = %v, want [3000]", got)
	}

	// Go superset: both Ports AND Publishers merged, deduped, sorted.
	both := map[string]any{
		"Ports":      []any{map[string]any{"container_port": float64(3000)}},
		"Publishers": []any{map[string]any{"TargetPort": float64(8080)}},
	}
	if got := containerPortsFromRaw(both); !reflect.DeepEqual(got, []int{3000, 8080}) {
		t.Fatalf("merged Ports+Publishers = %v, want [3000 8080]", got)
	}

	// Go superset: ContainerPort / target keys and docker-style mapping strings.
	extra := map[string]any{
		"Ports": []any{
			map[string]any{"ContainerPort": float64(5432)},
			map[string]any{"target": float64(6379)},
			"0.0.0.0:18080->8080/tcp",
		},
	}
	if got := containerPortsFromRaw(extra); !reflect.DeepEqual(got, []int{5432, 6379, 8080}) {
		t.Fatalf("extra keys/string = %v, want [5432 6379 8080]", got)
	}

	// Zero/invalid ports are skipped.
	if got := containerPortsFromRaw(map[string]any{"Ports": []any{map[string]any{"container_port": float64(0)}}}); len(got) != 0 {
		t.Fatalf("zero port should be skipped, got %v", got)
	}
}

// TestParsePortMappingStringMatchesDockerPsForm pins parsePortMappingString, the
// docker/podman ps "Ports" string parser used by collectContainerPorts: it
// requires the "->" arrow, takes the last colon segment of the host side and the
// pre-"/" segment of the target side.
func TestParsePortMappingStringMatchesDockerPsForm(t *testing.T) {
	cases := []struct {
		in     string
		host   int
		target int
	}{
		{"0.0.0.0:18080->8080/tcp", 18080, 8080},
		{"18080->8080", 18080, 8080},
		{"[::]:18080->8080/tcp", 18080, 8080},
		{"8080/tcp", 0, 0}, // no arrow -> skipped
		{"", 0, 0},
	}
	for _, tc := range cases {
		host, target := parsePortMappingString(tc.in)
		if host != tc.host || target != tc.target {
			t.Fatalf("parsePortMappingString(%q) = (%d,%d), want (%d,%d)", tc.in, host, target, tc.host, tc.target)
		}
	}
}

// TestMergePortHintsDedupesAndSorts locks mergePortHints: union of all groups,
// dropping non-positive ports, deduped and sorted ascending.
func TestMergePortHintsDedupesAndSorts(t *testing.T) {
	got := mergePortHints([]int{8080, 3000}, []int{3000, 0, 5432}, nil, []int{-1, 9090})
	if !reflect.DeepEqual(got, []int{3000, 5432, 8080, 9090}) {
		t.Fatalf("mergePortHints = %v, want [3000 5432 8080 9090]", got)
	}
	if got := mergePortHints(); !reflect.DeepEqual(got, []int{}) {
		t.Fatalf("mergePortHints() = %v, want []", got)
	}
}
