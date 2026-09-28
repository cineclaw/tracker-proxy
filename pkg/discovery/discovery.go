package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"tracker-proxy/pkg/version"
)

// DiscoveryMessage is the payload sent by clients seeking CineClaw servers.
type DiscoveryMessage struct {
	Cmd string `json:"cmd"` // "discover" or "ping"
}

// DiscoveryResponse is returned to clients containing server information.
type DiscoveryResponse struct {
	Status   string `json:"status"`
	Service  string `json:"service"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	HTTPPort int    `json:"http_port"`
}

// Server handles incoming UDP discovery broadcasts on local network.
type Server struct {
	port     int
	httpPort int
}

// NewServer creates a new UDP Discovery server.
func NewServer(udpPort, httpPort int) *Server {
	if udpPort <= 0 {
		udpPort = 9118
	}
	if httpPort <= 0 {
		httpPort = 3000
	}
	return &Server{
		port:     udpPort,
		httpPort: httpPort,
	}
}

// Start begins listening for UDP discovery broadcasts.
func (s *Server) Start(ctx context.Context) {
	addr := net.UDPAddr{
		Port: s.port,
		IP:   net.IPv4zero,
	}

	conn, err := net.ListenUDP("udp4", &addr)
	if err != nil {
		log.Printf("[Discovery] Failed to bind UDP discovery port %d: %v", s.port, err)
		return
	}
	defer conn.Close()

	log.Printf("[Discovery] Local UDP beacon responder listening on :%d (HTTP port %d)", s.port, s.httpPort)

	buf := make([]byte, 1024)

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		raw := strings.TrimSpace(string(buf[:n]))
		// Handle both JSON and simple text "cineclaw:discover" / "discover"
		isDiscovery := false
		if strings.Contains(raw, "discover") || strings.Contains(raw, "cineclaw") {
			isDiscovery = true
		} else {
			var msg DiscoveryMessage
			if err := json.Unmarshal(buf[:n], &msg); err == nil && (msg.Cmd == "discover" || msg.Cmd == "ping") {
				isDiscovery = true
			}
		}

		if isDiscovery {
			resp := DiscoveryResponse{
				Status:   "ok",
				Service:  "cineclaw",
				Name:     "CineClaw Cinema",
				Version:  version.Version,
				HTTPPort: s.httpPort,
			}
			data, err := json.Marshal(resp)
			if err == nil {
				_, _ = conn.WriteToUDP(data, remoteAddr)
			}
		}
	}
}

// ProbeLocalSubnet helper for clients or diagnostics to broadcast discovery packet.
func ProbeLocalSubnet(targetBroadcast string, port int, timeout time.Duration) ([]DiscoveryResponse, error) {
	if targetBroadcast == "" {
		targetBroadcast = "255.255.255.255"
	}
	conn, err := net.Dial("udp4", fmt.Sprintf("%s:%d", targetBroadcast, port))
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	msg := DiscoveryMessage{Cmd: "discover"}
	data, _ := json.Marshal(msg)
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var responses []DiscoveryResponse
	buf := make([]byte, 1024)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		var resp DiscoveryResponse
		if err := json.Unmarshal(buf[:n], &resp); err == nil && resp.Service == "cineclaw" {
			responses = append(responses, resp)
		}
	}

	return responses, nil
}
