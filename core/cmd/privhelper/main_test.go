package main

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

func TestCappedBuffer_TruncatesAndSaysSo(t *testing.T) {
	b := &cappedBuffer{limit: 10}
	b.Write([]byte("0123456789abcdef"))
	b.Write([]byte("more"))
	if got := b.String(); !strings.HasPrefix(got, "0123456789") || !strings.Contains(got, "truncated") || strings.Contains(got, "abc") {
		t.Errorf("got %q", got)
	}
	small := &cappedBuffer{limit: 100}
	small.Write([]byte("ok"))
	if small.String() != "ok" {
		t.Errorf("under the limit nothing changes, got %q", small.String())
	}
}

func TestReadInput_OnlyForPersistAndBounded(t *testing.T) {
	noInput, _ := privhelper.Validate([]string{"systemctl", "daemon-reload"})
	if data, err := readInput(noInput, strings.NewReader("ignored")); err != nil || data != nil {
		t.Errorf("a command without input must not read stdin: %q, %v", data, err)
	}
	put, err := privhelper.Validate([]string{"gateway-key", "put", "jwt-signing-key.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := readInput(put, strings.NewReader("-----BEGIN-----")); err != nil || string(data) != "-----BEGIN-----" {
		t.Fatalf("gateway-key put dropped its PEM: %q, %v", data, err)
	}
	persist, _ := privhelper.Validate([]string{"wireguard", "persist-peers"})
	if _, err := readInput(persist, strings.NewReader(strings.Repeat("x", privhelper.MaxRequestBytes+1))); err == nil {
		t.Error("input over MaxRequestBytes must be refused")
	}
}

func TestAllowedCaller_RootAlways(t *testing.T) {
	if err := allowedCaller(0); err != nil {
		t.Errorf("root must be allowed: %v", err)
	}
}

// The cap is enforced where the file is written, however the request arrived.
func TestDeploy_RefusesOversizedSecretBeforeWriting(t *testing.T) {
	resp := deploy([]string{"set-env", "acme-web"}, make([]byte, privhelper.MaxDeploySecretBytes+1))
	if resp.ExitCode != privhelper.ExitRefused || !strings.Contains(resp.Output, "limit") {
		t.Fatalf("got %+v, want a refusal naming the limit", resp)
	}
}
