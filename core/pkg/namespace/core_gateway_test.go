package namespace

import "testing"

func TestIsIndexGateway(t *testing.T) {
	if IsIndexGateway("") {
		t.Fatal("empty namespace is not index")
	}
	if IsIndexGateway("anchat-test") {
		t.Fatal("tenant gateway must not be index")
	}
	if !IsIndexGateway(BlueprintNameIndex) {
		t.Fatal("client_namespace=index must be the core gateway")
	}
}
