package node

import "testing"

// A load generator signs and broadcasts through the client's own codec and RPC connection, so the
// accessors must hand out the parts the client itself uses.
func TestClientAccessors_exposeTheCodecAndTheRPCConnection(t *testing.T) {
	c, err := Dial("tcp://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if c.RPC() == nil || c.TxBuilder() == nil || c.TxConfig() == nil {
		t.Fatalf("accessors returned nil: rpc=%v builder=%v config=%v", c.RPC(), c.TxBuilder(), c.TxConfig())
	}
	if c.RPC() != c.rpc || c.TxBuilder() != c.builder {
		t.Fatal("the accessors must return the client's own connection and builder, not copies")
	}
	if _, err := Dial(" "); err == nil {
		t.Fatal("an empty address was accepted")
	}
}

func TestClientAccount_anUnreachableNodeIsAnError(t *testing.T) {
	c, err := Dial("tcp://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Account(t.Context(), "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a"); err == nil {
		t.Fatal("an account lookup against a dead node reported success")
	}
	if _, err := c.BaseFee(t.Context()); err == nil {
		t.Fatal("a base fee lookup against a dead node reported success")
	}
}
