package types

import "testing"

// The Msg service is the three archiver messages. There is no UpdateParams and
// no authority signer.
func TestMsgServiceHasNoAdmin(t *testing.T) {
	var names []string
	for _, method := range _Msg_serviceDesc.Methods {
		names = append(names, method.MethodName)
	}
	if len(names) != 3 || names[0] != "Attest" || names[1] != "AttachReplicas" || names[2] != "CreateArchiveDeal" {
		t.Fatalf("Msg methods = %v, want Attest, AttachReplicas and CreateArchiveDeal only", names)
	}
}
