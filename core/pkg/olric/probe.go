package olric

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	olriclib "github.com/olric-data/olric"
	"github.com/olric-data/olric/config"
)

// Members asks the Olric member serving clients at hostPort for the cluster's members. Olric 0.7
// speaks its own protocol on that port and has no HTTP API, so a health probe has to be a client
// call: an HTTP GET there only ever reads a protocol error.
func Members(ctx context.Context, hostPort string, timeout time.Duration) ([]olriclib.Member, error) {
	cfg := config.NewClient()
	cfg.DialTimeout = timeout
	cfg.ReadTimeout = timeout
	cfg.WriteTimeout = timeout
	cfg.MaxRetries = -1
	client, err := olriclib.NewClusterClient([]string{hostPort},
		olriclib.WithLogger(log.New(io.Discard, "", 0)),
		olriclib.WithConfig(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("olric at %s: %w", hostPort, err)
	}
	defer client.Close(ctx)
	members, err := client.Members(ctx)
	if err != nil {
		return nil, fmt.Errorf("olric at %s: %w", hostPort, err)
	}
	return members, nil
}
