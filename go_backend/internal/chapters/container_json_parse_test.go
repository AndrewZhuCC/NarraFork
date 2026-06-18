package chapters

import (
	"testing"
)

// TestParseContainerJSONListStripsProviderNoise is a regression test for a real
// Go-vs-TS parity bug: runLimitedEnv combines stdout+stderr, so when the compose
// provider (e.g. podman-compose) prints ANSI escapes and warning lines into the
// stream, the JSON payload returned by `compose ps --format json` is prefixed
// with non-JSON noise. The previous parser failed json.Unmarshal on the whole
// blob (and the first '[' it would find was inside the ANSI escape "\x1b[4m"),
// returning 0 items — so a successful `compose up` persisted NO container
// instances. TS reads clean stdout and does not hit this. The parser now strips
// ANSI escapes and scans delimiters to recover the embedded JSON.
func TestParseContainerJSONListStripsProviderNoise(t *testing.T) {
	jsonArray := `[{"Id":"abc123","Image":"docker.io/library/golang:1.26-alpine","Names":["x_web_1"],"State":"running"}]`

	cases := []struct {
		name string
		out  string
	}{
		{"clean array", jsonArray},
		{
			"ansi + provider warning prefix",
			"\x1b[4m>>>> Executing external compose provider \"/home/u/.local/bin/podman-compose\". <<<<\x1b[0m\n" +
				"WARNING:podman_compose:unknown PODMAN_COMPOSE_ key [project_name] in environment\n" +
				jsonArray,
		},
		{
			"leading blank lines and ansi reset only",
			"\x1b[0m\n\n" + jsonArray,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := parseContainerJSONList(tc.out)
			if len(items) != 1 {
				t.Fatalf("expected 1 parsed item, got %d (input had provider noise that must be stripped)", len(items))
			}
			if items[0]["Id"] != "abc123" {
				t.Fatalf("parsed item missing expected Id: %#v", items[0])
			}
			norm := normalizeContainerInstance("chap1", items[0])
			if norm["status"] != "running" {
				t.Fatalf("normalized status = %v, want running", norm["status"])
			}
			if cid, _ := norm["containerId"].(string); cid != "abc123" {
				t.Fatalf("normalized containerId = %v, want abc123", norm["containerId"])
			}
		})
	}

	// Pure noise with no JSON must yield no items (not panic).
	if items := parseContainerJSONList("\x1b[4mwarning only, no json\x1b[0m\n"); len(items) != 0 {
		t.Fatalf("noise-only input should yield 0 items, got %d", len(items))
	}
}

// TestStripANSIEscapes covers the ANSI/CSI stripping helper directly.
func TestStripANSIEscapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"\x1b[4mbold\x1b[0m", "bold"},
		{"a\x1b[1;31mb\x1b[0mc", "abc"},
		{"[not-ansi]", "[not-ansi]"},
	}
	for _, tc := range cases {
		if got := stripANSIEscapes(tc.in); got != tc.want {
			t.Fatalf("stripANSIEscapes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseExposedPortsObjectStripsProviderNoise is a regression for the same
// combined-stdout+stderr root cause as parseContainerJSONList, applied to
// `podman inspect --format {{json .Config.ExposedPorts}}`: a warning/ANSI prefix
// must not break the JSON object parse (which would silently drop all ports and
// break proxy route resolution).
func TestParseExposedPortsObjectStripsProviderNoise(t *testing.T) {
	obj := `{"8080/tcp":{},"9090/tcp":{}}`
	cases := []struct {
		name string
		out  string
	}{
		{"clean object", obj},
		{"ansi + warning prefix", "\x1b[4mWARNING: rootless networking notice\x1b[0m\n" + obj},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parseExposedPortsObject(tc.out)
			if parsed == nil {
				t.Fatalf("parseExposedPortsObject returned nil for input with provider noise")
			}
			if _, ok := parsed["8080/tcp"]; !ok {
				t.Fatalf("parsed object missing expected port key: %#v", parsed)
			}
		})
	}
	if parseExposedPortsObject("\x1b[0mwarning only, no json\n") != nil {
		t.Fatalf("noise-only input should yield nil object")
	}
}

// TestMergeAllocatedHostPorts is a regression for the legacy-mode host-port
// parity bug: compose ps does not surface the host port allocated for non-proxy
// mode, so the runtime must merge the allocated mapping's hostPort onto the
// matching-service instance (mirroring TS _recordContainerInstances'
// userMapping?.hostPort fallback) before persisting.
func TestMergeAllocatedHostPorts(t *testing.T) {
	mappings := []map[string]any{
		{"serviceName": "web", "hostPort": 13456, "containerPort": 8080},
		{"serviceName": "api", "hostPort": 13457, "containerPort": 9090},
	}

	items := []map[string]any{
		{"serviceName": "web", "hostPort": nil, "containerPort": nil},
		{"serviceName": "api", "hostPort": 0, "containerPort": 0},
		{"serviceName": "other", "hostPort": nil},
	}
	mergeAllocatedHostPorts(items, mappings)

	if got := intValue(items[0]["hostPort"]); got != 13456 {
		t.Fatalf("web hostPort = %d, want 13456", got)
	}
	if got := intValue(items[0]["containerPort"]); got != 8080 {
		t.Fatalf("web containerPort = %d, want 8080", got)
	}
	if got := intValue(items[1]["hostPort"]); got != 13457 {
		t.Fatalf("api hostPort = %d, want 13457", got)
	}
	// Service with no allocated mapping is left untouched.
	if items[2]["hostPort"] != nil {
		t.Fatalf("unmatched service hostPort should stay nil, got %v", items[2]["hostPort"])
	}

	// An item that already has a published host port from ps must not be overwritten.
	pub := []map[string]any{{"serviceName": "web", "hostPort": 22222, "containerPort": 8080}}
	mergeAllocatedHostPorts(pub, mappings)
	if got := intValue(pub[0]["hostPort"]); got != 22222 {
		t.Fatalf("published hostPort should be preserved, got %d", got)
	}
}
