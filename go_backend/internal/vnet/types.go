package vnet

type Transport string

const (
	TransportRelay Transport = "relay"
	TransportUDP   Transport = "udp"
)

type Endpoint struct {
	Transport  Transport `json:"transport"`
	Address    string    `json:"address,omitempty"`
	Port       int       `json:"port,omitempty"`
	ObservedAt int64     `json:"observedAt,omitempty"`
}

type PeerInfo struct {
	PeerID       string         `json:"peerId"`
	VirtualIP    string         `json:"virtualIp"`
	DisplayName  string         `json:"displayName,omitempty"`
	Endpoints    []Endpoint     `json:"endpoints"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
	LastSeenAt   int64          `json:"lastSeenAt"`
}

type RelayAuth struct {
	Kind   string `json:"kind"`
	UserID string `json:"userId,omitempty"`
}

type UDPInfo struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type Options struct {
	Enabled            bool
	AllowAnonymous     bool
	RelayToken         string
	MaxPeersPerNetwork int
	MaxMessageBytes    int
	UDP                UDPInfo
}

type Stats struct {
	Networks           int     `json:"networks"`
	Peers              int     `json:"peers"`
	Connections        int     `json:"connections"`
	MaxPeersPerNetwork int     `json:"maxPeersPerNetwork"`
	MaxMessageBytes    int     `json:"maxMessageBytes"`
	UDP                UDPInfo `json:"udp"`
}

const (
	DefaultMaxPeersPerNetwork = 64
	DefaultMaxMessageBytes    = 1024 * 1024
)
