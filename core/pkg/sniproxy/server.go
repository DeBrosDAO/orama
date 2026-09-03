package sniproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Config tunes the proxy server.
type Config struct {
	// ClientHelloTimeout bounds the wait for a parseable ClientHello.
	// 0 selects 5 seconds.
	ClientHelloTimeout time.Duration
	// BackendDialTimeout bounds backend connect time. 0 selects 5 seconds.
	BackendDialTimeout time.Duration
	// MaxConcurrentConns caps total in-flight connections to prevent
	// resource exhaustion. 0 selects 10000.
	MaxConcurrentConns int
	// MaxConnsPerIP caps in-flight connections from one client IP.
	// 0 selects 32.
	MaxConnsPerIP int
	// IdleTimeout is the read/write idle deadline during the copy.
	// 0 selects 60 seconds.
	IdleTimeout time.Duration
}

// Server is a TCP-level SNI router. Create via NewServer, then call
// Serve(listener) in a goroutine. Close cancels in-flight connections.
type Server struct {
	router *Router
	cfg    Config
	logger *zap.Logger

	gate   chan struct{} // bounded semaphore for concurrent connections
	perIP  map[string]chan struct{}
	perIPN int
	mu     sync.Mutex
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewServer constructs a Server with the given router and config.
func NewServer(router *Router, cfg Config, logger *zap.Logger) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.ClientHelloTimeout <= 0 {
		cfg.ClientHelloTimeout = 5 * time.Second
	}
	if cfg.BackendDialTimeout <= 0 {
		cfg.BackendDialTimeout = 5 * time.Second
	}
	if cfg.MaxConcurrentConns <= 0 {
		cfg.MaxConcurrentConns = 10000
	}
	if cfg.MaxConnsPerIP <= 0 {
		cfg.MaxConnsPerIP = 32
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 60 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		router: router,
		cfg:    cfg,
		logger: logger.Named("sniproxy"),
		gate:   make(chan struct{}, cfg.MaxConcurrentConns),
		perIP:  make(map[string]chan struct{}),
		perIPN: cfg.MaxConnsPerIP,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) ipSlot(ip string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.perIP[ip]
	if ch == nil {
		ch = make(chan struct{}, s.perIPN)
		s.perIP[ip] = ch
	}
	return ch
}

// Serve accepts connections from ln until ln.Accept returns a permanent
// error or Close is called. Serve always returns a non-nil error.
func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check for shutdown via cancelled ctx.
			if s.ctx.Err() != nil {
				return s.ctx.Err()
			}
			// Net errors temporarily? Backoff briefly so we don't busy-loop.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		select {
		case s.gate <- struct{}{}:
		default:
			s.logger.Warn("max concurrent connections reached, dropping",
				zap.Int("limit", s.cfg.MaxConcurrentConns),
				zap.String("remote", conn.RemoteAddr().String()),
			)
			conn.Close()
			continue
		}
		ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		if ip == "" {
			ip = conn.RemoteAddr().String()
		}
		slot := s.ipSlot(ip)
		select {
		case slot <- struct{}{}:
		default:
			<-s.gate
			s.logger.Warn("max connections per IP reached, dropping",
				zap.Int("limit", s.perIPN),
				zap.String("ip", ip),
			)
			conn.Close()
			continue
		}
		s.wg.Add(1)
		go func(c net.Conn, ipSlot chan struct{}) {
			defer s.wg.Done()
			defer func() { <-s.gate }()
			defer func() { <-ipSlot }()
			s.handle(c)
		}(conn, slot)
	}
}

// Close cancels in-flight connections and waits for handlers to drain.
func (s *Server) Close() {
	s.cancel()
	s.wg.Wait()
}

// handle processes a single accepted connection: peek SNI, dial backend,
// replay peeked bytes, then bidirectional copy.
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	sni, peeked, err := PeekClientHello(conn, s.cfg.ClientHelloTimeout)
	if err != nil {
		s.logger.Debug("ClientHello peek failed",
			zap.String("remote", conn.RemoteAddr().String()),
			zap.Error(err),
		)
		return
	}

	backend := s.router.Pick(sni)
	if backend.Addr == "" {
		s.logger.Warn("no backend for SNI",
			zap.String("sni", sni),
			zap.String("remote", conn.RemoteAddr().String()),
		)
		return
	}

	network := backend.Network
	if network == "" {
		network = "tcp"
	}

	upstream, err := net.DialTimeout(network, backend.Addr, s.cfg.BackendDialTimeout)
	if err != nil {
		s.logger.Warn("backend dial failed",
			zap.String("sni", sni),
			zap.String("backend", backend.Addr),
			zap.Error(err),
		)
		return
	}
	defer upstream.Close()

	// Replay peeked bytes (the ClientHello + anything else buffered).
	if len(peeked) > 0 {
		if _, err := upstream.Write(peeked); err != nil {
			s.logger.Debug("replay to backend failed",
				zap.String("sni", sni),
				zap.Error(err),
			)
			return
		}
	}

	src := idleConn{Conn: conn, idle: s.cfg.IdleTimeout}
	dst := idleConn{Conn: upstream, idle: s.cfg.IdleTimeout}

	// Bidirectional copy. We close both connections when either side
	// finishes OR when the server is shutting down, so handle() can't
	// hang forever on a half-stuck peer.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(src, dst)
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-s.ctx.Done():
	}
	// Force both sides closed; second copy will exit immediately.
	upstream.Close()
	conn.Close()
	<-done // drain the second goroutine
}

// idleConn resets the deadline on every Read/Write so a stalled peer
// cannot hold a backend slot forever (bugboard #117).
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	if c.idle > 0 {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	}
	return c.Conn.Read(p)
}

func (c idleConn) Write(p []byte) (int, error) {
	if c.idle > 0 {
		_ = c.Conn.SetWriteDeadline(time.Now().Add(c.idle))
	}
	return c.Conn.Write(p)
}
