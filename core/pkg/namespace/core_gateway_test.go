package namespace

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway"
)

func TestIsIndexGateway(t *testing.T) {
	if IsIndexGateway(nil) {
		t.Fatal("nil config is not index")
	}
	if IsIndexGateway(&gateway.Config{ClientNamespace: "anchat-test", GlobalRQLiteDSN: "http://localhost:5001"}) {
		t.Fatal("tenant gateway must not be index")
	}
	if !IsIndexGateway(&gateway.Config{ClientNamespace: BlueprintNameIndex}) {
		t.Fatal("client_namespace=index must be the core gateway")
	}
}
