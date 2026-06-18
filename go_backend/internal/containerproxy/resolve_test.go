package containerproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestResolveTargetNestedDomainsPrefersDotlessLabel verifies the proxy host
// resolution disambiguates overlapping project domains (e.g. example.test vs
// dev.example.test) the same way the TypeScript resolveLabel does: a candidate
// is only accepted when the remaining label has no dot, so a host under the
// deeper domain never mis-resolves to the shallower one. With two projects whose
// domains overlap, a request for <label>.dev.example.test must route to the
// dev.example.test project's container, not the example.test project's.
func TestResolveTargetNestedDomainsPrefersDotlessLabel(t *testing.T) {
	database := openProxyTestDB(t)
	// p1 already has dev.example.test (from openProxyTestDB). Add a project with
	// the shallower overlapping domain and its own container.
	if _, err := database.Exec(`INSERT INTO projects (id, proxy_domain) VALUES ('p2', 'example.test')`); err != nil {
		t.Fatalf("insert p2: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id) VALUES ('c2', 'p2')`); err != nil {
		t.Fatalf("insert c2: %v", err)
	}

	deepUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("deep"))
	}))
	defer deepUpstream.Close()
	shallowUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("shallow"))
	}))
	defer shallowUpstream.Close()

	deepIP, deepPort := hostPortFromURL(t, deepUpstream.URL)
	shallowIP, shallowPort := hostPortFromURL(t, shallowUpstream.URL)
	// Deep container under dev.example.test (c1/p1).
	seedProxyTarget(t, database, deepIP, deepPort)
	// Shallow container under example.test (c2/p2) with its own label.
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_ip, container_port, proxy_label) VALUES ('ci2', 'c2', 'api', 'running', ?, ?, 'shallow12-api-9000')`, shallowIP, shallowPort); err != nil {
		t.Fatalf("insert shallow target: %v", err)
	}

	proxy := httptest.NewServer(New(database, nil))
	defer proxy.Close()

	// A request whose host sits under the DEEP domain must hit the deep upstream.
	// (If resolution wrongly matched example.test first, the label would be
	// "abc12345-web-8080.dev" which contains a dot and must be rejected, so it
	// would never resolve to the shallow project.)
	body := proxyGet(t, proxy.URL, "abc12345-web-8080.dev.example.test")
	if body != "deep" {
		t.Fatalf("nested-domain host routed to %q, want deep", body)
	}
	// A request under the shallow domain with a dotless label hits the shallow one.
	body = proxyGet(t, proxy.URL, "shallow12-api-9000.example.test")
	if body != "shallow" {
		t.Fatalf("shallow-domain host routed to %q, want shallow", body)
	}
}

// TestResolveTargetRejectsDottedLabel ensures a host whose label segment still
// contains a dot after stripping the domain suffix is not resolved (mirrors the
// TS `label.includes(".")` guard), returning 502 rather than mis-routing. The
// container row is deliberately seeded with a proxy_label EQUAL to the dotted
// strip result, so only the dotless guard prevents a (wrong) match — making this
// test fail if the guard is removed.
func TestResolveTargetRejectsDottedLabel(t *testing.T) {
	database := openProxyTestDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("should-not-reach"))
	}))
	defer upstream.Close()
	ip, port := hostPortFromURL(t, upstream.URL)
	// proxy_label equals what "extra.abc12345-web-8080.dev.example.test" reduces
	// to after stripping ".dev.example.test"; without the dotless guard this would
	// wrongly resolve and route to the upstream.
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_ip, container_port, proxy_label) VALUES ('cidotted', 'c1', 'web', 'running', ?, ?, 'extra.abc12345-web-8080')`, ip, port); err != nil {
		t.Fatalf("insert dotted-label target: %v", err)
	}
	proxy := httptest.NewServer(New(database, nil))
	defer proxy.Close()

	// "extra.abc12345-web-8080" is a dotted label under dev.example.test.
	status := proxyGetStatus(t, proxy.URL, "extra.abc12345-web-8080.dev.example.test")
	if status != http.StatusBadGateway {
		t.Fatalf("dotted-label host status = %d, want 502", status)
	}
}

// TestResolveTargetIgnoresNonRunningAndEmptyIP ensures the target query only
// matches running instances with a non-empty container IP (paused/stopped or
// IP-less rows must not be proxied), matching the targetForLabel SQL guards. The
// paused row points at a REACHABLE upstream so that only the status guard (not a
// failed forward) can produce the 502, keeping the test guard-dependent.
func TestResolveTargetIgnoresNonRunningAndEmptyIP(t *testing.T) {
	database := openProxyTestDB(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("reachable"))
	}))
	defer upstream.Close()
	ip, port := hostPortFromURL(t, upstream.URL)
	proxy := httptest.NewServer(New(database, nil))
	defer proxy.Close()

	// Paused instance pointing at the reachable upstream -> must not resolve
	// (status guard), so the proxy returns 502 rather than 200 "reachable".
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_ip, container_port, proxy_label) VALUES ('cipaused', 'c1', 'web', 'paused', ?, ?, 'abc12345-web-8080')`, ip, port); err != nil {
		t.Fatalf("insert paused: %v", err)
	}
	if status := proxyGetStatus(t, proxy.URL, "abc12345-web-8080.dev.example.test"); status != http.StatusBadGateway {
		t.Fatalf("paused-instance host status = %d, want 502", status)
	}

	// Flip it to running but with an empty IP -> still must not resolve (IP guard).
	if _, err := database.Exec(`UPDATE container_instances SET status = 'running', container_ip = '' WHERE id = 'cipaused'`); err != nil {
		t.Fatalf("update empty ip: %v", err)
	}
	if status := proxyGetStatus(t, proxy.URL, "abc12345-web-8080.dev.example.test"); status != http.StatusBadGateway {
		t.Fatalf("empty-ip running-instance host status = %d, want 502", status)
	}
}

func proxyGet(t *testing.T, proxyURL, host string) string {
	t.Helper()
	resp := doProxyRequest(t, proxyURL, host)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy status = %d for host %q", resp.StatusCode, host)
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func proxyGetStatus(t *testing.T, proxyURL, host string) int {
	t.Helper()
	resp := doProxyRequest(t, proxyURL, host)
	defer resp.Body.Close()
	return resp.StatusCode
}

func doProxyRequest(t *testing.T, proxyURL, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, proxyURL+"/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	return resp
}
