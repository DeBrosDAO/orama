// Package turn provides a built-in TURN/STUN server using Pion.
package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pion/turn/v4"
	"go.uber.org/zap"
)

// Config contains TURN server configuration
type Config struct {
	// Enabled enables the built-in TURN server
	Enabled bool `yaml:"enabled"`

	// ListenAddr is the UDP address to listen on (e.g., "0.0.0.0:3478")
	ListenAddr string `yaml:"listen_addr"`

	// PublicIP is the public IP address to advertise for relay
	// If empty, will try to auto-detect
	PublicIP string `yaml:"public_ip"`

	// Realm is the TURN realm (e.g., "orama.network")
	Realm string `yaml:"realm"`

	// SharedSecret is the secret for HMAC-SHA1 credential generation
	// Should match the gateway's TURN_SHARED_SECRET
	SharedSecret string `yaml:"shared_secret"`

	// CredentialTTL is the lifetime of generated credentials
	CredentialTTL time.Duration `yaml:"credential_ttl"`

	// MinPort and MaxPort define the relay port range
	MinPort uint16 `yaml:"min_port"`
	MaxPort uint16 `yaml:"max_port"`

	// TLS Configuration for TURNS (TURN over TLS)
	// TLSEnabled enables TURNS listener on TLSListenAddr
	TLSEnabled bool `yaml:"tls_enabled"`

	// TLSListenAddr is the TCP/TLS address to listen on (e.g., "0.0.0.0:443")
	TLSListenAddr string `yaml:"tls_listen_addr"`

	// TLSCertFile is the path to the TLS certificate file
	TLSCertFile string `yaml:"tls_cert_file"`

	// TLSKeyFile is the path to the TLS private key file
	TLSKeyFile string `yaml:"tls_key_file"`
}

// DefaultConfig returns a default TURN server configuration
func DefaultConfig() *Config {
	return &Config{
		Enabled:       false,
		ListenAddr:    "0.0.0.0:3478",
		Realm:         "orama.network",
		CredentialTTL: 24 * time.Hour,
		MinPort:       49152,
		MaxPort:       65535,
	}
}

// Server is a built-in TURN/STUN server
type Server struct {
	config      *Config
	logger      *zap.Logger
	turnServer  *turn.Server
	conn        net.PacketConn // UDP listener
	tlsListener net.Listener   // TLS listener for TURNS
	mu          sync.RWMutex
	running     bool
}

// NewServer creates a new TURN server
func NewServer(config *Config, logger *zap.Logger) (*Server, error) {
	if config == nil {
		config = DefaultConfig()
	}

	return &Server{
		config: config,
		logger: logger,
	}, nil
}

// Start starts the TURN server
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	if !s.config.Enabled {
		s.logger.Info("TURN server disabled")
		return nil
	}

	if s.config.SharedSecret == "" {
		return fmt.Errorf("TURN shared secret is required")
	}

	// Create UDP listener
	conn, err := net.ListenPacket("udp4", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.ListenAddr, err)
	}
	s.conn = conn

	// Determine public IP
	publicIP := s.config.PublicIP
	if publicIP == "" {
		// Try to auto-detect
		publicIP, err = getPublicIP()
		if err != nil {
			s.logger.Warn("Failed to auto-detect public IP, using listener address", zap.Error(err))
			host, _, _ := net.SplitHostPort(s.config.ListenAddr)
			if host == "0.0.0.0" || host == "" {
				host = "127.0.0.1"
			}
			publicIP = host
		}
	}

	relayIP := net.ParseIP(publicIP)
	if relayIP == nil {
		return fmt.Errorf("invalid public IP: %s", publicIP)
	}

	s.logger.Info("Starting TURN server",
		zap.String("listen_addr", s.config.ListenAddr),
		zap.String("public_ip", publicIP),
		zap.String("realm", s.config.Realm),
		zap.Uint16("min_port", s.config.MinPort),
		zap.Uint16("max_port", s.config.MaxPort),
		zap.Bool("tls_enabled", s.config.TLSEnabled),
	)

	// Prepare listener configs for TLS (TURNS)
	var listenerConfigs []turn.ListenerConfig

	if s.config.TLSEnabled && s.config.TLSCertFile != "" && s.config.TLSKeyFile != "" {
		// Load TLS certificate
		cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
		if err != nil {
			conn.Close()
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}

		// Determine TLS listen address
		tlsListenAddr := s.config.TLSListenAddr
		if tlsListenAddr == "" {
			tlsListenAddr = "0.0.0.0:443"
		}

		// Create TLS listener
		tlsListener, err := tls.Listen("tcp", tlsListenAddr, tlsConfig)
		if err != nil {
			conn.Close()
			return fmt.Errorf("failed to start TLS listener on %s: %w", tlsListenAddr, err)
		}
		s.tlsListener = tlsListener

		listenerConfigs = append(listenerConfigs, turn.ListenerConfig{
			Listener: tlsListener,
			RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
				RelayAddress: relayIP,
				Address:      "0.0.0.0",
				MinPort:      s.config.MinPort,
				MaxPort:      s.config.MaxPort,
			},
		})

		s.logger.Info("TURNS (TLS) listener started",
			zap.String("tls_addr", tlsListenAddr),
		)
	}

	// Create TURN server with HMAC-SHA1 auth
	turnServer, err := turn.NewServer(turn.ServerConfig{
		Realm: s.config.Realm,
		AuthHandler: func(username, realm string, srcAddr net.Addr) ([]byte, bool) {
			return s.authHandler(username, realm, srcAddr)
		},
		PacketConnConfigs: []turn.PacketConnConfig{
			{
				PacketConn: conn,
				RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
					RelayAddress: relayIP,
					Address:      "0.0.0.0",
					MinPort:      s.config.MinPort,
					MaxPort:      s.config.MaxPort,
				},
			},
		},
		ListenerConfigs: listenerConfigs,
	})
	if err != nil {
		conn.Close()
		if s.tlsListener != nil {
			s.tlsListener.Close()
		}
		return fmt.Errorf("failed to create TURN server: %w", err)
	}

	s.turnServer = turnServer
	s.running = true

	s.logger.Info("TURN server started successfully",
		zap.String("addr", s.config.ListenAddr),
		zap.String("realm", s.config.Realm),
		zap.Bool("turns_enabled", s.config.TLSEnabled),
	)

	return nil
}

// authHandler validates HMAC-SHA1 credentials (coturn-compatible format)
// Username format: timestamp:userID (e.g., "1234567890:user123")
func (s *Server) authHandler(username, realm string, srcAddr net.Addr) ([]byte, bool) {
	// Parse timestamp from username
	// Format: timestamp:userID
	var timestamp int64
	for i, c := range username {
		if c == ':' {
			ts, err := strconv.ParseInt(username[:i], 10, 64)
			if err != nil {
				s.logger.Debug("Invalid timestamp in username", zap.String("username", username))
				return nil, false
			}
			timestamp = ts
			break
		}
	}

	// Check if credential has expired
	now := time.Now().Unix()
	if timestamp > 0 && timestamp < now {
		s.logger.Debug("Credential expired",
			zap.String("username", username),
			zap.Int64("expired_at", timestamp),
			zap.Int64("now", now),
		)
		return nil, false
	}

	// Generate expected password using HMAC-SHA1
	// This matches the gateway's generateTURNCredentials function
	h := hmac.New(sha1.New, []byte(s.config.SharedSecret))
	h.Write([]byte(username))
	password := base64.StdEncoding.EncodeToString(h.Sum(nil))

	s.logger.Debug("TURN auth request",
		zap.String("username", username),
		zap.String("realm", realm),
		zap.String("src_addr", srcAddr.String()),
	)

	return turn.GenerateAuthKey(username, realm, password), true
}

// Stop stops the TURN server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.logger.Info("Stopping TURN server")

	if s.turnServer != nil {
		if err := s.turnServer.Close(); err != nil {
			s.logger.Warn("Error closing TURN server", zap.Error(err))
		}
		s.turnServer = nil
	}

	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}

	// Close TLS listener
	if s.tlsListener != nil {
		s.tlsListener.Close()
		s.tlsListener = nil
	}

	s.running = false
	s.logger.Info("TURN server stopped")

	return nil
}

// IsRunning returns whether the server is running
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetListenAddr returns the listen address
func (s *Server) GetListenAddr() string {
	return s.config.ListenAddr
}

// GetPublicAddr returns the public address for clients
func (s *Server) GetPublicAddr() string {
	if s.config.PublicIP != "" {
		_, port, _ := net.SplitHostPort(s.config.ListenAddr)
		return net.JoinHostPort(s.config.PublicIP, port)
	}
	return s.config.ListenAddr
}

// getPublicIP tries to determine the public IP address
func getPublicIP() (string, error) {
	// Try to get outbound IP by connecting to a public address
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}
