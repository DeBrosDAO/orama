package types

import "testing"

// The Msg service is the two archiver messages. There is no UpdateParams and
// no authority signer.
func TestMsgServiceHasNoAdmin(t *testing.T) {
	var names []string
	for _, method := range _Msg_serviceDesc.Methods {
		names = append(names, method.MethodName)
	}
	if len(names) != 2 || names[0] != "Attest" || names[1] != "AttachReplicas" {
		t.Fatalf("Msg methods = %v, want Attest and AttachReplicas only", names)
	}
}
