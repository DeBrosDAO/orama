package rqlite

import (
	"fmt"
	"strconv"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"go.uber.org/zap"
)

func init() {
	plugin.Register("rqlite", setup)
}

// setup configures the rqlite plugin
func setup(c *caddy.Controller) error {
	p, err := parseConfig(c)
	if err != nil {
		return plugin.Error("rqlite", err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		p.Next = next
		return p
	})

	return nil
}

// parseConfig parses the Corefile configuration
func parseConfig(c *caddy.Controller) (*RQLitePlugin, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	var (
		dsn         = "http://localhost:5001"
		refreshRate = 10 * time.Second
		cacheTTL    = 300 * time.Second
		cacheSize   = 10000
		zones       []string
	)

	// Parse zone arguments
	for c.Next() {
		// Note: c.Val() returns the plugin name "rqlite", not the zone
		// Get zones from remaining args or server block keys
		zones = append(zones, plugin.OriginsFromArgsOrServerBlock(c.RemainingArgs(), c.ServerBlockKeys)...)

		// Parse plugin configuration block
		for c.NextBlock() {
			switch c.Val() {
			case "dsn":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				dsn = c.Val()

			case "refresh":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				dur, err := time.ParseDuration(c.Val())
				if err != nil {
					return nil, fmt.Errorf("invalid refresh duration: %w", err)
				}
				refreshRate = dur

			case "ttl":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				ttlVal, err := strconv.Atoi(c.Val())
				if err != nil {
					return nil, fmt.Errorf("invalid TTL: %w", err)
				}
				cacheTTL = time.Duration(ttlVal) * time.Second

			case "cache_size":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				size, err := strconv.Atoi(c.Val())
				if err != nil {
					return nil, fmt.Errorf("invalid cache size: %w", err)
				}
				cacheSize = size

			default:
				return nil, c.Errf("unknown property '%s'", c.Val())
			}
		}
	}

	if len(zones) == 0 {
		zones = []string{"."}
	}

	// Create backend
	backend, err := NewBackend(dsn, refreshRate, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create backend: %w", err)
	}

	// Create cache
	cache := NewCache(cacheSize, cacheTTL)

	logger.Info("RQLite plugin initialized",
		zap.String("dsn", dsn),
		zap.Duration("refresh", refreshRate),
		zap.Duration("cache_ttl", cacheTTL),
		zap.Int("cache_size", cacheSize),
		zap.Strings("zones", zones),
	)

	return &RQLitePlugin{
		logger:  logger,
		backend: backend,
		cache:   cache,
		zones:   zones,
	}, nil
}
