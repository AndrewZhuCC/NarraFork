package vnet

import (
	"sync"
	"time"
)

type Socket interface {
	SendJSON(payload any)
	Close(code int, reason string)
}

type Connection struct {
	Socket       Socket
	Auth         RelayAuth
	NetworkID    string
	Peer         *PeerInfo
	SessionID    string
	UDPAuthToken string
	ConnectedAt  int64
	LastSeenAt   int64
}

type NetworkState struct {
	Peers map[string]*Connection
}

type RelayHub struct {
	mu          sync.Mutex
	connections map[Socket]*Connection
	networks    map[string]*NetworkState
	sessions    map[string]*Connection
	options     Options
}

func NewRelayHub(options Options) *RelayHub {
	hub := &RelayHub{
		connections: map[Socket]*Connection{},
		networks:    map[string]*NetworkState{},
		sessions:    map[string]*Connection{},
	}
	hub.Configure(options)
	return hub
}

func DefaultOptions() Options {
	return Options{Enabled: true, MaxPeersPerNetwork: DefaultMaxPeersPerNetwork, MaxMessageBytes: DefaultMaxMessageBytes, UDP: UDPInfo{Enabled: false}}
}

func (h *RelayHub) Configure(options Options) {
	if options.MaxPeersPerNetwork <= 0 {
		options.MaxPeersPerNetwork = DefaultMaxPeersPerNetwork
	}
	if options.MaxMessageBytes <= 0 {
		options.MaxMessageBytes = DefaultMaxMessageBytes
	}
	h.mu.Lock()
	h.options = options
	h.mu.Unlock()
}

func (h *RelayHub) AttachSocket(socket Socket, auth RelayAuth) {
	if socket == nil {
		return
	}
	now := nowMillis()
	h.mu.Lock()
	h.connections[socket] = &Connection{Socket: socket, Auth: auth, ConnectedAt: now, LastSeenAt: now}
	h.mu.Unlock()
}

func (h *RelayHub) DetachSocket(socket Socket) {
	if socket == nil {
		return
	}
	var broadcasts []queuedMessage
	h.mu.Lock()
	conn := h.connections[socket]
	if conn == nil {
		h.mu.Unlock()
		return
	}
	delete(h.connections, socket)
	if conn.SessionID != "" {
		delete(h.sessions, conn.SessionID)
	}
	if conn.NetworkID != "" && conn.Peer != nil {
		if network := h.networks[conn.NetworkID]; network != nil {
			if current := network.Peers[conn.Peer.PeerID]; current == conn {
				delete(network.Peers, conn.Peer.PeerID)
				broadcasts = h.broadcastLocked(conn.NetworkID, map[string]any{"type": "peer_left", "peerId": conn.Peer.PeerID}, conn)
				if len(network.Peers) == 0 {
					delete(h.networks, conn.NetworkID)
				}
			}
		}
	}
	h.mu.Unlock()
	sendQueued(broadcasts)
}

func (h *RelayHub) HandleClientMessage(socket Socket, message map[string]any) {
	if socket == nil {
		return
	}
	var sends []queuedMessage
	var closes []queuedClose
	h.mu.Lock()
	conn := h.connections[socket]
	if conn == nil {
		h.mu.Unlock()
		socket.SendJSON(errorMessage("NOT_ATTACHED", "Relay socket is not attached"))
		return
	}
	if JSONByteLength(message) > h.options.MaxMessageBytes {
		h.mu.Unlock()
		socket.SendJSON(errorMessage("MESSAGE_TOO_LARGE", "Message is too large"))
		return
	}
	if err := ValidateClientMessage(message); err != nil {
		h.mu.Unlock()
		socket.SendJSON(errorMessage("INVALID_MESSAGE", "Invalid vnet message"))
		return
	}
	conn.LastSeenAt = nowMillis()
	switch StringValue(message["type"]) {
	case "hello":
		sends, closes = h.handleHelloLocked(conn, message)
	case "relay_packet":
		sends = h.handleRelayPacketLocked(conn, message)
	case "punch_request":
		sends = h.handlePunchRequestLocked(conn, message)
	case "pong":
	}
	h.mu.Unlock()
	sendQueued(sends)
	for _, closeItem := range closes {
		closeItem.socket.Close(closeItem.code, closeItem.reason)
	}
}

func (h *RelayHub) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	peers := 0
	for _, network := range h.networks {
		peers += len(network.Peers)
	}
	return Stats{Networks: len(h.networks), Peers: peers, Connections: len(h.connections), MaxPeersPerNetwork: h.options.MaxPeersPerNetwork, MaxMessageBytes: h.options.MaxMessageBytes, UDP: h.options.UDP}
}

func (h *RelayHub) handleHelloLocked(conn *Connection, message map[string]any) ([]queuedMessage, []queuedClose) {
	if conn.Peer != nil {
		h.detachLocked(conn)
		now := nowMillis()
		conn = &Connection{Socket: conn.Socket, Auth: conn.Auth, ConnectedAt: now, LastSeenAt: now}
		h.connections[conn.Socket] = conn
	}
	networkID := StringValue(message["networkId"])
	peerID := StringValue(message["peerId"])
	virtualIP := StringValue(message["virtualIp"])
	network := h.networks[networkID]
	if network == nil {
		network = &NetworkState{Peers: map[string]*Connection{}}
		h.networks[networkID] = network
	}
	sends := []queuedMessage{}
	closes := []queuedClose{}
	if previous := network.Peers[peerID]; previous != nil {
		sends = append(sends, queuedMessage{socket: previous.Socket, payload: errorMessage("PEER_REPLACED", "Peer was replaced by a newer connection")})
		closes = append(closes, queuedClose{socket: previous.Socket, code: 1000, reason: "peer replaced"})
		h.detachLocked(previous)
		network = h.networks[networkID]
		if network == nil {
			network = &NetworkState{Peers: map[string]*Connection{}}
			h.networks[networkID] = network
		}
	}
	for _, existing := range network.Peers {
		if existing.Peer != nil && existing.Peer.VirtualIP == virtualIP && existing.Peer.PeerID != peerID {
			return []queuedMessage{{socket: conn.Socket, payload: errorMessage("VIRTUAL_IP_IN_USE", "VNet virtual IP is already in use by another peer")}}, closes
		}
	}
	if len(network.Peers) >= h.options.MaxPeersPerNetwork {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("NETWORK_FULL", "VNet network has reached its peer limit")}}, closes
	}
	now := nowMillis()
	endpoints := ParseEndpoints(message["endpoints"])
	if len(endpoints) == 0 {
		endpoints = []Endpoint{{Transport: TransportRelay, ObservedAt: now}}
	}
	capabilities, _ := message["capabilities"].(map[string]any)
	peer := &PeerInfo{PeerID: peerID, VirtualIP: virtualIP, DisplayName: StringValue(message["displayName"]), Endpoints: endpoints, Capabilities: capabilities, LastSeenAt: now}
	sessionID := RandomToken(18)
	udpAuthToken := RandomToken(32)
	conn.NetworkID = networkID
	conn.Peer = peer
	conn.SessionID = sessionID
	conn.UDPAuthToken = udpAuthToken
	conn.LastSeenAt = now
	network.Peers[peerID] = conn
	h.sessions[sessionID] = conn
	peers := []PeerInfo{}
	for _, existing := range network.Peers {
		if existing != conn && existing.Peer != nil {
			peers = append(peers, publicPeerInfo(existing.Peer))
		}
	}
	sends = append(sends, queuedMessage{socket: conn.Socket, payload: map[string]any{"type": "welcome", "networkId": networkID, "peerId": peerID, "sessionId": sessionID, "udpAuthToken": udpAuthToken, "udp": h.options.UDP, "peers": peers}})
	sends = append(sends, h.broadcastLocked(networkID, map[string]any{"type": "peer_joined", "peer": publicPeerInfo(peer)}, conn)...)
	return sends, closes
}

func (h *RelayHub) handleRelayPacketLocked(conn *Connection, message map[string]any) []queuedMessage {
	if conn.NetworkID == "" || conn.Peer == nil {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("NOT_REGISTERED", "Send hello first")}}
	}
	network := h.networks[conn.NetworkID]
	if network == nil {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("PEER_NOT_FOUND", "Target peer not found")}}
	}
	var target *Connection
	if toPeerID := StringValue(message["toPeerId"]); toPeerID != "" {
		target = network.Peers[toPeerID]
	} else if dstIP := StringValue(message["dstIp"]); dstIP != "" {
		for _, item := range network.Peers {
			if item.Peer != nil && item.Peer.VirtualIP == dstIP {
				target = item
				break
			}
		}
	}
	if target == nil || target.Peer == nil {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("PEER_NOT_FOUND", "Target peer not found")}}
	}
	if srcIP := StringValue(message["srcIp"]); srcIP != "" && srcIP != conn.Peer.VirtualIP {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("INVALID_SOURCE_IP", "Relay packet source IP must match the sending peer")}}
	}
	if dstIP := StringValue(message["dstIp"]); dstIP != "" && dstIP != target.Peer.VirtualIP {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("INVALID_DESTINATION_IP", "Relay packet destination IP must match the target peer")}}
	}
	payload := map[string]any{}
	for key, value := range message {
		payload[key] = value
	}
	payload["fromPeerId"] = conn.Peer.PeerID
	payload["srcIp"] = conn.Peer.VirtualIP
	payload["dstIp"] = target.Peer.VirtualIP
	return []queuedMessage{{socket: target.Socket, payload: payload}}
}

func (h *RelayHub) handlePunchRequestLocked(conn *Connection, message map[string]any) []queuedMessage {
	if conn.NetworkID == "" || conn.Peer == nil {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("NOT_REGISTERED", "Send hello first")}}
	}
	network := h.networks[conn.NetworkID]
	if network == nil {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("PEER_NOT_FOUND", "Target peer not found")}}
	}
	target := network.Peers[StringValue(message["toPeerId"])]
	if target == nil {
		return []queuedMessage{{socket: conn.Socket, payload: errorMessage("PEER_NOT_FOUND", "Target peer not found")}}
	}
	tid := StringValue(message["tid"])
	if tid == "" {
		tid = RandomToken(12)
	}
	payload := map[string]any{"type": "punch_offer", "fromPeerId": conn.Peer.PeerID, "tid": tid}
	if endpoint, ok := findUDPEndpoint(conn.Peer); ok {
		payload["endpoint"] = endpoint
	}
	return []queuedMessage{{socket: target.Socket, payload: payload}}
}

func (h *RelayHub) detachLocked(conn *Connection) []queuedMessage {
	if conn == nil {
		return nil
	}
	delete(h.connections, conn.Socket)
	if conn.SessionID != "" {
		delete(h.sessions, conn.SessionID)
	}
	if conn.NetworkID == "" || conn.Peer == nil {
		return nil
	}
	network := h.networks[conn.NetworkID]
	if network == nil || network.Peers[conn.Peer.PeerID] != conn {
		return nil
	}
	delete(network.Peers, conn.Peer.PeerID)
	messages := h.broadcastLocked(conn.NetworkID, map[string]any{"type": "peer_left", "peerId": conn.Peer.PeerID}, conn)
	if len(network.Peers) == 0 {
		delete(h.networks, conn.NetworkID)
	}
	return messages
}

func (h *RelayHub) broadcastLocked(networkID string, payload any, except *Connection) []queuedMessage {
	network := h.networks[networkID]
	if network == nil {
		return nil
	}
	messages := []queuedMessage{}
	for _, conn := range network.Peers {
		if conn == except {
			continue
		}
		messages = append(messages, queuedMessage{socket: conn.Socket, payload: payload})
	}
	return messages
}

func publicPeerInfo(peer *PeerInfo) PeerInfo {
	if peer == nil {
		return PeerInfo{}
	}
	endpoints := append([]Endpoint{}, peer.Endpoints...)
	capabilities := map[string]any(nil)
	if peer.Capabilities != nil {
		capabilities = map[string]any{}
		for key, value := range peer.Capabilities {
			capabilities[key] = value
		}
	}
	return PeerInfo{PeerID: peer.PeerID, VirtualIP: peer.VirtualIP, DisplayName: peer.DisplayName, Endpoints: endpoints, Capabilities: capabilities, LastSeenAt: peer.LastSeenAt}
}

func findUDPEndpoint(peer *PeerInfo) (Endpoint, bool) {
	if peer == nil {
		return Endpoint{}, false
	}
	for _, endpoint := range peer.Endpoints {
		if endpoint.Transport == TransportUDP && endpoint.Address != "" {
			return endpoint, true
		}
	}
	return Endpoint{}, false
}

func errorMessage(code, message string) map[string]any {
	return map[string]any{"type": "error", "code": code, "message": message}
}

type queuedMessage struct {
	socket  Socket
	payload any
}

type queuedClose struct {
	socket Socket
	code   int
	reason string
}

func sendQueued(messages []queuedMessage) {
	for _, message := range messages {
		message.socket.SendJSON(message.payload)
	}
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
