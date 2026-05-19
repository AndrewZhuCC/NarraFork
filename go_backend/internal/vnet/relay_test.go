package vnet

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

type fakeSocket struct {
	mu       sync.Mutex
	messages []map[string]any
	closed   bool
	code     int
	reason   string
}

func (s *fakeSocket) SendJSON(payload any) {
	data, _ := json.Marshal(payload)
	var msg map[string]any
	_ = json.Unmarshal(data, &msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}

func (s *fakeSocket) Close(code int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.code = code
	s.reason = reason
}

func (s *fakeSocket) last() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return nil
	}
	return s.messages[len(s.messages)-1]
}

func (s *fakeSocket) messageOfType(kind string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, msg := range s.messages {
		if msg["type"] == kind {
			return msg
		}
	}
	return nil
}

func TestRelayHelloWelcomeJoinAndDetachCleanup(t *testing.T) {
	hub := NewRelayHub(Options{Enabled: true, MaxPeersPerNetwork: 4, MaxMessageBytes: 4096})
	a := &fakeSocket{}
	b := &fakeSocket{}
	hub.AttachSocket(a, RelayAuth{Kind: "anonymous"})
	hub.AttachSocket(b, RelayAuth{Kind: "anonymous"})

	hub.HandleClientMessage(a, map[string]any{"type": "hello", "networkId": "net", "peerId": "a", "virtualIp": "10.0.0.1"})
	welcomeA := a.messageOfType("welcome")
	if welcomeA == nil || welcomeA["networkId"] != "net" || welcomeA["peerId"] != "a" || welcomeA["sessionId"] == "" {
		t.Fatalf("unexpected welcome: %#v", welcomeA)
	}

	hub.HandleClientMessage(b, map[string]any{"type": "hello", "networkId": "net", "peerId": "b", "virtualIp": "10.0.0.2", "endpoints": []any{map[string]any{"transport": "udp", "address": "1.2.3.4", "port": float64(9999)}}})
	if joined := a.messageOfType("peer_joined"); joined == nil {
		t.Fatalf("peer a did not receive peer_joined: %#v", a.messages)
	}
	welcomeB := b.messageOfType("welcome")
	peers, _ := welcomeB["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("expected b welcome to include a as existing peer, got %#v", welcomeB)
	}
	stats := hub.Stats()
	if stats.Networks != 1 || stats.Peers != 2 || stats.Connections != 2 {
		t.Fatalf("unexpected stats after hello: %#v", stats)
	}

	hub.DetachSocket(b)
	if left := a.messageOfType("peer_left"); left == nil || left["peerId"] != "b" {
		t.Fatalf("peer a did not receive peer_left for b: %#v", a.messages)
	}
	hub.DetachSocket(a)
	stats = hub.Stats()
	if stats.Networks != 0 || stats.Peers != 0 || stats.Connections != 0 {
		t.Fatalf("detach should clean relay state, got %#v", stats)
	}
}

func TestRelayPacketAndPunchRequest(t *testing.T) {
	hub := NewRelayHub(Options{Enabled: true, MaxPeersPerNetwork: 4, MaxMessageBytes: 4096})
	a := &fakeSocket{}
	b := &fakeSocket{}
	hub.AttachSocket(a, RelayAuth{Kind: "anonymous"})
	hub.AttachSocket(b, RelayAuth{Kind: "anonymous"})
	hub.HandleClientMessage(a, map[string]any{"type": "hello", "networkId": "net", "peerId": "a", "virtualIp": "10.0.0.1", "endpoints": []any{map[string]any{"transport": "udp", "address": "1.1.1.1", "port": float64(1000)}}})
	hub.HandleClientMessage(b, map[string]any{"type": "hello", "networkId": "net", "peerId": "b", "virtualIp": "10.0.0.2"})

	hub.HandleClientMessage(a, map[string]any{"type": "relay_packet", "toPeerId": "b", "seq": float64(1), "nonce": "n", "ciphertext": "c"})
	relayed := b.messageOfType("relay_packet")
	if relayed == nil || relayed["fromPeerId"] != "a" || relayed["srcIp"] != "10.0.0.1" || relayed["dstIp"] != "10.0.0.2" {
		t.Fatalf("unexpected relayed packet: %#v", relayed)
	}

	hub.HandleClientMessage(a, map[string]any{"type": "punch_request", "toPeerId": "b", "tid": "tid-1"})
	offer := b.messageOfType("punch_offer")
	if offer == nil || offer["fromPeerId"] != "a" || offer["tid"] != "tid-1" {
		t.Fatalf("unexpected punch offer: %#v", offer)
	}
	endpoint, _ := offer["endpoint"].(map[string]any)
	if endpoint["transport"] != "udp" || endpoint["address"] != "1.1.1.1" {
		t.Fatalf("punch offer should include sender UDP endpoint, got %#v", offer)
	}
}

func TestRelayErrorsPeerReplacementAndLimits(t *testing.T) {
	t.Run("replacement and virtual ip conflict", func(t *testing.T) {
		hub := NewRelayHub(Options{Enabled: true, MaxPeersPerNetwork: 4, MaxMessageBytes: 4096})
		a1 := &fakeSocket{}
		a2 := &fakeSocket{}
		b := &fakeSocket{}
		hub.AttachSocket(a1, RelayAuth{Kind: "anonymous"})
		hub.AttachSocket(a2, RelayAuth{Kind: "anonymous"})
		hub.AttachSocket(b, RelayAuth{Kind: "anonymous"})
		helloA := map[string]any{"type": "hello", "networkId": "net", "peerId": "a", "virtualIp": "10.0.0.1"}
		hub.HandleClientMessage(a1, helloA)
		hub.HandleClientMessage(a2, helloA)
		if msg := a1.messageOfType("error"); msg == nil || msg["code"] != "PEER_REPLACED" || !a1.closed {
			t.Fatalf("old peer should be replaced and closed, msg=%#v closed=%v", msg, a1.closed)
		}
		hub.HandleClientMessage(b, map[string]any{"type": "hello", "networkId": "net", "peerId": "b", "virtualIp": "10.0.0.1"})
		if msg := b.last(); msg == nil || msg["code"] != "VIRTUAL_IP_IN_USE" {
			t.Fatalf("expected VIRTUAL_IP_IN_USE, got %#v", msg)
		}
	})

	t.Run("packet validation errors and limits", func(t *testing.T) {
		hub := NewRelayHub(Options{Enabled: true, MaxPeersPerNetwork: 2, MaxMessageBytes: 4096})
		a := &fakeSocket{}
		b := &fakeSocket{}
		c := &fakeSocket{}
		hub.AttachSocket(a, RelayAuth{Kind: "anonymous"})
		hub.AttachSocket(b, RelayAuth{Kind: "anonymous"})
		hub.AttachSocket(c, RelayAuth{Kind: "anonymous"})
		hub.HandleClientMessage(a, map[string]any{"type": "relay_packet", "toPeerId": "missing", "seq": float64(1), "nonce": "n", "ciphertext": "c"})
		if msg := a.last(); msg["code"] != "NOT_REGISTERED" {
			t.Fatalf("expected NOT_REGISTERED, got %#v", msg)
		}
		hub.HandleClientMessage(a, map[string]any{"type": "hello", "networkId": "net", "peerId": "a", "virtualIp": "10.0.0.1"})
		hub.HandleClientMessage(b, map[string]any{"type": "hello", "networkId": "net", "peerId": "b", "virtualIp": "10.0.0.2"})
		if msg := b.messageOfType("welcome"); msg == nil {
			t.Fatalf("expected b to join before the network fills, got %#v", b.messages)
		}
		hub.HandleClientMessage(c, map[string]any{"type": "hello", "networkId": "net", "peerId": "c", "virtualIp": "10.0.0.3"})
		if msg := c.last(); msg["code"] != "NETWORK_FULL" {
			t.Fatalf("expected NETWORK_FULL, got %#v", msg)
		}
		hub.HandleClientMessage(c, map[string]any{"type": "relay_packet", "toPeerId": "a", "seq": float64(1), "nonce": "n", "ciphertext": strings.Repeat("x", 8192)})
		if msg := c.last(); msg["code"] != "MESSAGE_TOO_LARGE" {
			t.Fatalf("expected MESSAGE_TOO_LARGE, got %#v", msg)
		}
		hub.Configure(Options{Enabled: true, MaxPeersPerNetwork: 4, MaxMessageBytes: 4096})
		hub.HandleClientMessage(a, map[string]any{"type": "unknown"})
		if msg := a.last(); msg["code"] != "INVALID_MESSAGE" {
			t.Fatalf("expected INVALID_MESSAGE, got %#v", msg)
		}
		hub.HandleClientMessage(a, map[string]any{"type": "relay_packet", "toPeerId": "missing", "seq": float64(1), "nonce": "n", "ciphertext": "c"})
		if msg := a.last(); msg["code"] != "PEER_NOT_FOUND" {
			t.Fatalf("expected PEER_NOT_FOUND, got %#v", msg)
		}
		hub.HandleClientMessage(a, map[string]any{"type": "relay_packet", "dstIp": "10.0.0.2", "srcIp": "10.0.0.9", "seq": float64(1), "nonce": "n", "ciphertext": "c"})
		if msg := a.last(); msg["code"] != "INVALID_SOURCE_IP" {
			t.Fatalf("expected INVALID_SOURCE_IP, got %#v", msg)
		}
	})
}

func TestRandomTokenIsBase64URLish(t *testing.T) {
	token := RandomToken(18)
	if token == "" || strings.ContainsAny(token, "+/=") {
		t.Fatalf("unexpected token format: %q", token)
	}
}
