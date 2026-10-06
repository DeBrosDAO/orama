package sfu

import (
	"errors"
	"io"

	"github.com/pion/interceptor"
	"go.uber.org/zap"
)

// rtpSource is a publisher's incoming track.
type rtpSource interface {
	Read(b []byte) (int, interceptor.Attributes, error)
}

// rtpSink is the local track fanned out to every subscriber.
type rtpSink interface {
	Write(b []byte) (int, error)
}

// forwardRTP copies RTP from src to dst until src ends (publisher left or its
// connection closed). A failed Write never ends it: dst fans a packet out to
// every subscriber binding and returns the failures of the bindings that
// failed, so one subscriber's error says nothing about the others, who still
// received the packet. io.ErrClosedPipe is a subscriber that has already
// closed - expected, so not worth a log line.
//
// drop, when set, is asked per packet: a packet it says to drop is read and
// not forwarded. That is how a muted publisher's audio stops here, on the
// server, whatever the publisher's own client does.
func forwardRTP(src rtpSource, dst rtpSink, logger *zap.Logger, drop func() bool) {
	buf := make([]byte, rtpBufferSize)
	for {
		n, _, err := src.Read(buf)
		if err != nil {
			return
		}
		if drop != nil && drop() {
			continue
		}
		if _, err := dst.Write(buf[:n]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			logger.Debug("Forwarding to a subscriber failed", zap.Error(err))
		}
	}
}
