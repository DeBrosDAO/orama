package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	pionTurn "github.com/pion/turn/v4"
	"go.uber.org/zap"
)

// Server wraps a Pion TURN server with namespace-scoped HMAC-SHA1 authentication.
type Server struct {
	config     *Config
	logger     *zap.Logger
	turnServer *pionTurn.Server
	conn       net.PacketConn // UDP listener on primary port (3478)
	tlsConn    net.PacketConn // UDP listener on TLS port (443)
}

// NewServer creates and starts a TURN server.
func NewServer(cfg *Config, logger *zap.Logger) (*Server, error) {
	if errs := cfg.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("invalid TURN config: %v", errs[0])
	}

	relayIP := net.ParseIP(cfg.PublicIP)
	if relayIP == nil {
		return nil, fmt.Errorf("turn.public_ip: %q is not a valid IP address", cfg.PublicIP)
	}

	s := &Server{
		config: cfg,
		logger: logger.With(zap.String("component", "turn"), zap.String("namespace", cfg.Namespace)),
	}

	// Create primary UDP listener (port 3478)
	conn, err := net.ListenPacket("udp4", cfg.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", cfg.ListenAddr, err)
	}
	s.conn = conn

	packetConfigs := []pionTurn.PacketConnConfig{
		{
			PacketConn: conn,
			RelayAddressGenerator: &pionTurn.RelayAddressGeneratorPortRange{
				RelayAddress: relayIP,
				Address:      "0.0.0.0",
				MinPort:      uint16(cfg.RelayPortStart),
				MaxPort:      uint16(cfg.RelayPortEnd),
			},
		},
	}

	// Create TLS UDP listener (port 443) if configured
	// UDP 443 does not conflict with Caddy's TCP 443
	if cfg.TLSListenAddr != "" {
		tlsConn, err := net.ListenPacket("udp4", cfg.TLSListenAddr)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed to listen on %s: %w", cfg.TLSListenAddr, err)
		}
		s.tlsConn = tlsConn

		packetConfigs = append(packetConfigs, pionTurn.PacketConnConfig{
			PacketConn: tlsConn,
			RelayAddressGenerator: &pionTurn.RelayAddressGeneratorPortRange{
				RelayAddress: relayIP,
				Address:      "0.0.0.0",
				MinPort:      uint16(cfg.RelayPortStart),
				MaxPort:      uint16(cfg.RelayPortEnd),
			},
		})
	}

	// Create TURN server with HMAC-SHA1 auth
	turnServer, err := pionTurn.NewServer(pionTurn.ServerConfig{
		Realm: cfg.Realm,
		AuthHandler: func(username, realm string, srcAddr net.Addr) ([]byte, bool) {
			return s.authHandler(username, realm, srcAddr)
		},
		PacketConnConfigs: packetConfigs,
	})
	if err != nil {
		s.closeListeners()
		return nil, fmt.Errorf("failed to create TURN server: %w", err)
	}
	s.turnServer = turnServer

	s.logger.Info("TURN server started",
		zap.String("listen_addr", cfg.ListenAddr),
		zap.String("tls_listen_addr", cfg.TLSListenAddr),
		zap.String("public_ip", cfg.PublicIP),
		zap.String("realm", cfg.Realm),
		zap.Int("relay_port_start", cfg.RelayPortStart),
		zap.Int("relay_port_end", cfg.RelayPortEnd),
	)

	return s, nil
}

// authHandler validates HMAC-SHA1 credentials.
// Username format: {expiry_unix}:{namespace}
// Password: base64(HMAC-SHA1(shared_secret, username))
func (s *Server) authHandler(username, realm string, srcAddr net.Addr) ([]byte, bool) {
	// Parse username: must be "{timestamp}:{namespace}"
	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		s.logger.Debug("Malformed TURN username: expected timestamp:namespace",
			zap.String("username", username),
			zap.String("src_addr", srcAddr.String()))
		return nil, false
	}

	timestamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		s.logger.Debug("Invalid timestamp in TURN username",
			zap.String("username", username),
			zap.String("src_addr", srcAddr.String()))
		return nil, false
	}

	ns := parts[1]

	// Verify namespace matches this TURN server's namespace
	if ns != s.config.Namespace {
		s.logger.Debug("TURN credential namespace mismatch",
			zap.String("credential_namespace", ns),
			zap.String("server_namespace", s.config.Namespace),
			zap.String("src_addr", srcAddr.String()))
		return nil, false
	}

	// Check expiry — credential must not be expired
	if timestamp <= time.Now().Unix() {
		s.logger.Debug("TURN credential expired",
			zap.String("username", username),
			zap.Int64("expired_at", timestamp),
			zap.String("src_addr", srcAddr.String()))
		return nil, false
	}

	// Generate expected password and derive auth key
	password := GeneratePassword(s.config.AuthSecret, username)
	key := pionTurn.GenerateAuthKey(username, realm, password)

	s.logger.Debug("TURN auth accepted",
		zap.String("namespace", ns),
		zap.String("src_addr", srcAddr.String()))

	return key, true
}

// Close gracefully shuts down the TURN server.
func (s *Server) Close() error {
	s.logger.Info("Stopping TURN server")

	if s.turnServer != nil {
		if err := s.turnServer.Close(); err != nil {
			s.logger.Warn("Error closing TURN server", zap.Error(err))
		}
	}

	s.closeListeners()

	s.logger.Info("TURN server stopped")
	return nil
}

func (s *Server) closeListeners() {
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	if s.tlsConn != nil {
		s.tlsConn.Close()
		s.tlsConn = nil
	}
}

// GenerateCredentials creates time-limited HMAC-SHA1 TURN credentials.
// Returns username and password suitable for WebRTC ICE server configuration.
func GenerateCredentials(secret, namespace string, ttl time.Duration) (username, password string) {
	expiry := time.Now().Add(ttl).Unix()
	username = fmt.Sprintf("%d:%s", expiry, namespace)
	password = GeneratePassword(secret, username)
	return username, password
}

// GeneratePassword computes the HMAC-SHA1 password for a TURN username.
func GeneratePassword(secret, username string) string {
	h := hmac.New(sha1.New, []byte(secret))
	h.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ValidateCredentials checks if TURN credentials are valid and not expired.
func ValidateCredentials(secret, username, password, expectedNamespace string) bool {
	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		return false
	}

	timestamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}

	// Check namespace
	if parts[1] != expectedNamespace {
		return false
	}

	// Check expiry
	if timestamp <= time.Now().Unix() {
		return false
	}

	// Check password
	expected := GeneratePassword(secret, username)
	return hmac.Equal([]byte(password), []byte(expected))
}
