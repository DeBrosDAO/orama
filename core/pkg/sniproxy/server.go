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
}

// Server is a TCP-level SNI router. Create via NewServer, then call
// Serve(listener) in a goroutine. Close cancels in-flight connections.
type Server struct {
	router  *Router
	cfg     Config
	logger  *zap.Logger

	gate   chan struct{}      // bounded semaphore for concurrent connections
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
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		router: router,
		cfg:    cfg,
		logger: logger.Named("sniproxy"),
		gate:   make(chan struct{}, cfg.MaxConcurrentConns),
		ctx:    ctx,
		cancel: cancel,
	}
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
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer func() { <-s.gate }()
			s.handle(c)
		}(conn)
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

	// Bidirectional copy. We close both connections when either side
	// finishes OR when the server is shutting down, so handle() can't
	// hang forever on a half-stuck peer.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
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
