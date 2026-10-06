package pubsub

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/contracttest"
)

// The body the TypeScript SDK sends to /v1/pubsub/publish must decode into the
// struct this handler parses, with nothing left over. See pkg/contracttest.
func TestPubSubContract(t *testing.T) {
	fixtures, err := contracttest.For(".", "pubsub/")
	if err != nil {
		t.Fatalf("load contracts: %v", err)
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			switch fixture.Route {
			case "/v1/pubsub/publish":
				var body PublishRequest
				if err := fixture.DecodeStrict(&body); err != nil {
					t.Fatalf("the gateway does not parse what %s sends: %v", fixture.SDK, err)
				}
				if body.Topic == "" {
					t.Error("publish: a topic is required")
				}
				// The payload is base64 on the wire in both directions: the SDK
				// encodes here and decodes on the socket, and a message with no
				// payload is not a message.
				if body.DataB64 == "" {
					t.Error("publish: data_base64 is required")
				}
			default:
				t.Fatalf("fixture %s names route %s, which this test does not know", fixture.Name, fixture.Route)
			}
		})
	}
}
