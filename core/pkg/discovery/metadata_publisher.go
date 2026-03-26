package discovery

import (
	"context"
	"encoding/json"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"go.uber.org/zap"
)

// MetadataProvider is implemented by subsystems that can supply node metadata.
// The publisher calls Provide() every cycle and stores the result in the peerstore.
type MetadataProvider interface {
	ProvideMetadata() *RQLiteNodeMetadata
}

// MetadataPublisher periodically writes local node metadata to the peerstore so
// it is included in every peer exchange response. This decouples metadata
// production (lifecycle, RQLite status, service health) from the exchange
// protocol itself.
type MetadataPublisher struct {
	host     host.Host
	provider MetadataProvider
	interval time.Duration
	logger   *zap.Logger
}

// NewMetadataPublisher creates a publisher that writes metadata every interval.
func NewMetadataPublisher(h host.Host, provider MetadataProvider, interval time.Duration, logger *zap.Logger) *MetadataPublisher {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &MetadataPublisher{
		host:     h,
		provider: provider,
		interval: interval,
		logger:   logger.With(zap.String("component", "metadata-publisher")),
	}
}

// Start begins the periodic publish loop. It blocks until ctx is cancelled.
func (p *MetadataPublisher) Start(ctx context.Context) {
	// Publish immediately on start
	p.publish()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.publish()
		}
	}
}

// PublishNow performs a single immediate metadata publish.
// Useful after lifecycle transitions or other state changes.
func (p *MetadataPublisher) PublishNow() {
	p.publish()
}

func (p *MetadataPublisher) publish() {
	meta := p.provider.ProvideMetadata()
	if meta == nil {
		return
	}

	data, err := json.Marshal(meta)
	if err != nil {
		p.logger.Error("Failed to marshal metadata", zap.Error(err))
		return
	}

	if err := p.host.Peerstore().Put(p.host.ID(), "rqlite_metadata", data); err != nil {
		p.logger.Error("Failed to store metadata in peerstore", zap.Error(err))
	}
}
