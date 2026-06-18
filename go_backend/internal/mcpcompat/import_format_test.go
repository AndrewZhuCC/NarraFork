package mcpcompat

import (
	"reflect"
	"testing"
)

// TestNormalizeImportServerMapFormats locks the MCP import format detection,
// mirroring the TypeScript /servers/import normalization in server/routes/mcp.ts:
// it accepts { mcpServers: {...} }, { servers: {...} }, a single-server config
// object (JetBrains style), and a bare { name: {...} } server map, and rejects
// anything else with a 400.
func TestNormalizeImportServerMapFormats(t *testing.T) {
	t.Run("mcpServers wrapper", func(t *testing.T) {
		root := map[string]any{"mcpServers": map[string]any{"a": map[string]any{"command": "node"}}}
		got, err := normalizeImportServerMap(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got["a"]; !ok || len(got) != 1 {
			t.Fatalf("expected server 'a', got %#v", got)
		}
	})

	t.Run("servers wrapper", func(t *testing.T) {
		root := map[string]any{"servers": map[string]any{"b": map[string]any{"url": "https://x"}}}
		got, err := normalizeImportServerMap(root)
		if err != nil || len(got) != 1 {
			t.Fatalf("servers wrapper: got %#v err %v", got, err)
		}
	})

	t.Run("single server config wrapped under derived name", func(t *testing.T) {
		root := map[string]any{"type": "sse", "url": "https://mcp.example.com/x"}
		got, err := normalizeImportServerMap(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got["mcp.example.com"]; !ok || len(got) != 1 {
			t.Fatalf("expected derived name 'mcp.example.com', got %#v", got)
		}
	})

	t.Run("bare server map", func(t *testing.T) {
		root := map[string]any{"srv1": map[string]any{"command": "node"}}
		got, err := normalizeImportServerMap(root)
		if err != nil || len(got) != 1 {
			t.Fatalf("bare server map: got %#v err %v", got, err)
		}
	})

	t.Run("unrecognized format rejected", func(t *testing.T) {
		root := map[string]any{"foo": "bar", "n": float64(1)}
		if _, err := normalizeImportServerMap(root); err == nil {
			t.Fatal("expected error for unrecognized format")
		}
	})
}

// TestIsSingleServerConfig covers each single-server detection branch.
func TestIsSingleServerConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want bool
	}{
		{"url field", map[string]any{"url": "https://x"}, true},
		{"command field", map[string]any{"command": "node"}, true},
		{"transport stdio", map[string]any{"transport": "stdio"}, true},
		{"type sse", map[string]any{"type": "sse"}, true},
		{"type streamable-http", map[string]any{"type": "streamable-http"}, true},
		{"unknown type", map[string]any{"type": "carrier-pigeon"}, false},
		{"empty", map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSingleServerConfig(tc.cfg); got != tc.want {
				t.Fatalf("isSingleServerConfig(%#v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestDeriveImportServerName covers name derivation from url host (with the
// localhost-port special case), command basename, and the imported-server
// fallback, matching server/routes/mcp.ts.
func TestDeriveImportServerName(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"url hostname", map[string]any{"url": "https://mcp.example.com/path"}, "mcp.example.com"},
		{"localhost with port", map[string]any{"url": "http://localhost:8931/sse"}, "localhost-8931"},
		{"command basename", map[string]any{"command": "/usr/local/bin/my-server"}, "my-server"},
		{"command windows path", map[string]any{"command": "C:\\bin\\srv.exe"}, "srv.exe"},
		{"command trailing slash", map[string]any{"command": "/opt/tool/"}, "tool"},
		{"no usable fields", map[string]any{"args": []any{"x"}}, "imported-server"},
		{"invalid url falls through to fallback", map[string]any{"url": "://nohost"}, "imported-server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveImportServerName(tc.cfg); got != tc.want {
				t.Fatalf("deriveImportServerName(%#v) = %q, want %q", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestDetectTransport locks transport inference: explicit transport/type wins,
// otherwise url-without-command implies streamable-http, and the default is
// stdio.
func TestDetectTransport(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"explicit sse", map[string]any{"transport": "sse"}, "sse"},
		{"explicit streamable-http", map[string]any{"type": "streamable-http"}, "streamable-http"},
		{"explicit stdio", map[string]any{"transport": "stdio"}, "stdio"},
		{"url without command implies http", map[string]any{"url": "https://x"}, "streamable-http"},
		{"command implies stdio", map[string]any{"command": "node"}, "stdio"},
		{"empty defaults stdio", map[string]any{}, "stdio"},
		{"url and command together defaults stdio", map[string]any{"url": "https://x", "command": "node"}, "stdio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectTransport(tc.cfg); got != tc.want {
				t.Fatalf("detectTransport(%#v) = %q, want %q", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestImportMapFiltersNonObjectEntries confirms importMap drops entries whose
// value is not an object and returns nil for a non-object input.
func TestImportMapFiltersNonObjectEntries(t *testing.T) {
	got := importMap(map[string]any{
		"good": map[string]any{"command": "node"},
		"bad":  "not-an-object",
	})
	want := map[string]map[string]any{"good": {"command": "node"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("importMap filtered = %#v, want %#v", got, want)
	}
	if got := importMap("not-a-map"); got != nil {
		t.Fatalf("importMap(non-map) = %#v, want nil", got)
	}
}
