package vnet

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

func JSONByteLength(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return math.MaxInt
	}
	return len(data)
}

func RandomToken(size int) string {
	if size <= 0 {
		size = 18
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func ValidateClientMessage(value map[string]any) error {
	typeValue := strings.TrimSpace(StringValue(value["type"]))
	if typeValue == "" {
		return fmt.Errorf("missing message type")
	}
	switch typeValue {
	case "hello":
		if StringValue(value["networkId"]) == "" || StringValue(value["peerId"]) == "" || StringValue(value["virtualIp"]) == "" {
			return fmt.Errorf("hello requires networkId, peerId, and virtualIp")
		}
		if displayName, ok := value["displayName"]; ok && displayName != nil {
			if _, ok := displayName.(string); !ok {
				return fmt.Errorf("displayName must be a string")
			}
		}
		if rawEndpoints, ok := value["endpoints"]; ok && rawEndpoints != nil {
			endpoints, ok := rawEndpoints.([]any)
			if !ok {
				return fmt.Errorf("endpoints must be an array")
			}
			for _, item := range endpoints {
				record, ok := item.(map[string]any)
				if !ok {
					return fmt.Errorf("endpoint must be an object")
				}
				transport := StringValue(record["transport"])
				if transport != string(TransportRelay) && transport != string(TransportUDP) {
					return fmt.Errorf("invalid endpoint transport")
				}
				if address, ok := record["address"]; ok && address != nil {
					if _, ok := address.(string); !ok {
						return fmt.Errorf("endpoint address must be a string")
					}
				}
				if port, ok := record["port"]; ok && port != nil && IntValue(port) <= 0 {
					return fmt.Errorf("endpoint port must be a positive number")
				}
				if observedAt, ok := record["observedAt"]; ok && observedAt != nil && Int64Value(observedAt) <= 0 {
					return fmt.Errorf("endpoint observedAt must be a positive number")
				}
			}
		}
		if capabilities, ok := value["capabilities"]; ok && capabilities != nil {
			if _, ok := capabilities.(map[string]any); !ok {
				return fmt.Errorf("capabilities must be an object")
			}
		}
		return nil
	case "relay_packet":
		if toPeerID, ok := value["toPeerId"]; ok && toPeerID != nil {
			if _, ok := toPeerID.(string); !ok {
				return fmt.Errorf("toPeerId must be a string")
			}
		}
		if dstIP, ok := value["dstIp"]; ok && dstIP != nil {
			if _, ok := dstIP.(string); !ok {
				return fmt.Errorf("dstIp must be a string")
			}
		}
		if srcIP, ok := value["srcIp"]; ok && srcIP != nil {
			if _, ok := srcIP.(string); !ok {
				return fmt.Errorf("srcIp must be a string")
			}
		}
		if srcPort, ok := value["srcPort"]; ok && srcPort != nil && IntValue(srcPort) <= 0 {
			return fmt.Errorf("srcPort must be a positive number")
		}
		if dstPort, ok := value["dstPort"]; ok && dstPort != nil && IntValue(dstPort) <= 0 {
			return fmt.Errorf("dstPort must be a positive number")
		}
		if Int64Value(value["seq"]) == 0 || StringValue(value["nonce"]) == "" || StringValue(value["ciphertext"]) == "" {
			return fmt.Errorf("relay_packet requires seq, nonce, and ciphertext")
		}
		return nil
	case "punch_request":
		if StringValue(value["toPeerId"]) == "" {
			return fmt.Errorf("punch_request requires toPeerId")
		}
		if tid, ok := value["tid"]; ok && tid != nil {
			if _, ok := tid.(string); !ok {
				return fmt.Errorf("tid must be a string")
			}
		}
		return nil
	case "pong":
		return nil
	default:
		return fmt.Errorf("unsupported vnet message type")
	}
}

func ParseEndpoints(value any) []Endpoint {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	endpoints := make([]Endpoint, 0, len(raw))
	for _, item := range raw {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		transport := Transport(StringValue(record["transport"]))
		if transport != TransportRelay && transport != TransportUDP {
			continue
		}
		endpoints = append(endpoints, Endpoint{
			Transport:  transport,
			Address:    StringValue(record["address"]),
			Port:       IntValue(record["port"]),
			ObservedAt: Int64Value(record["observedAt"]),
		})
	}
	return endpoints
}

func StringValue(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func IntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func Int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}
