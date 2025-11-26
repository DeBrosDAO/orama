package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// TCPSNIGateway handles SNI-based TCP routing for services like RQLite Raft, IPFS, etc.
type TCPSNIGateway struct {
	logger    *logging.ColoredLogger
	config    *config.SNIConfig
	listener  net.Listener
	routes    map[string]string
	mu        sync.RWMutex
	running   bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	tlsConfig *tls.Config
}

// NewTCPSNIGateway creates a new TCP SNI-based gateway
func NewTCPSNIGateway(logger *logging.ColoredLogger, cfg *config.SNIConfig) (*TCPSNIGateway, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if logger == nil {
		var err error
		logger, err = logging.NewColoredLogger(logging.ComponentGeneral, true)
		if err != nil {
			return nil, fmt.Errorf("failed to create logger: %w", err)
		}
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	gateway := &TCPSNIGateway{
		logger: logger,
		config: cfg,
		routes: make(map[string]string),
		ctx:    ctx,
		cancel: cancel,
		tlsConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}

	for hostname, backend := range cfg.Routes {
		gateway.routes[strings.ToLower(hostname)] = backend
	}

	logger.ComponentInfo(logging.ComponentGeneral, "TCP SNI Gateway initialized",
		zap.String("listen_addr", cfg.ListenAddr),
		zap.Int("routes", len(cfg.Routes)),
	)

	return gateway, nil
}

// Start starts the TCP SNI gateway server
func (g *TCPSNIGateway) Start(ctx context.Context) error {
	if g == nil || !g.config.Enabled {
		return nil
	}

	listener, err := tls.Listen("tcp", g.config.ListenAddr, g.tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", g.config.ListenAddr, err)
	}
	g.listener = listener
	g.running = true

	g.logger.ComponentInfo(logging.ComponentGeneral, "TCP SNI Gateway starting",
		zap.String("listen_addr", g.config.ListenAddr),
	)

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-g.ctx.Done():
					return
				default:
					g.logger.ComponentError(logging.ComponentGeneral, "Accept error", zap.Error(err))
					continue
				}
			}
			g.wg.Add(1)
			go func(c net.Conn) {
				defer g.wg.Done()
				g.handleConnection(c)
			}(conn)
		}
	}()

	select {
	case <-ctx.Done():
	case <-g.ctx.Done():
	}

	return g.Stop()
}

// handleConnection routes a TCP connection based on SNI
func (g *TCPSNIGateway) handleConnection(conn net.Conn) {
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		g.logger.ComponentError(logging.ComponentGeneral, "Expected TLS connection")
		return
	}

	if err := tlsConn.Handshake(); err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "TLS handshake failed", zap.Error(err))
		return
	}

	serverName := strings.ToLower(tlsConn.ConnectionState().ServerName)
	if serverName == "" {
		g.logger.ComponentError(logging.ComponentGeneral, "No SNI provided")
		return
	}

	g.mu.RLock()
	backend, found := g.routes[serverName]
	if !found {
		for prefix, be := range g.routes {
			if strings.HasPrefix(serverName, prefix+".") {
				backend = be
				found = true
				break
			}
		}
	}
	g.mu.RUnlock()

	if !found {
		g.logger.ComponentError(logging.ComponentGeneral, "No route for SNI",
			zap.String("server_name", serverName),
		)
		return
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "Routing connection",
		zap.String("server_name", serverName),
		zap.String("backend", backend),
	)

	backendConn, err := net.DialTimeout("tcp", backend, 10*time.Second)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "Backend connect failed",
			zap.String("backend", backend),
			zap.Error(err),
		)
		return
	}
	defer backendConn.Close()

	errc := make(chan error, 2)
	go func() { _, err := io.Copy(backendConn, tlsConn); errc <- err }()
	go func() { _, err := io.Copy(tlsConn, backendConn); errc <- err }()
	<-errc
}

// Stop gracefully stops the TCP SNI gateway
func (g *TCPSNIGateway) Stop() error {
	if g == nil || !g.running {
		return nil
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "TCP SNI Gateway shutting down")
	g.cancel()

	if g.listener != nil {
		g.listener.Close()
	}

	done := make(chan struct{})
	go func() { g.wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		g.logger.ComponentWarn(logging.ComponentGeneral, "Shutdown timeout")
	}

	g.running = false
	g.logger.ComponentInfo(logging.ComponentGeneral, "TCP SNI Gateway shutdown complete")
	return nil
}

