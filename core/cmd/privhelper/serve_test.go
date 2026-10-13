package main

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// A unit that is not orama-node or the index gateway must not reach execute,
// even with an argv Validate accepts.
func TestHandle_enforcesAuthorize(t *testing.T) {
	resp, err := handle(privhelper.Caller{
		UID:  1000,
		Unit: "orama-deploy-node@alice-web.service",
	}, privhelper.Request{
		Argv: []string{"systemctl", "start", "orama-deploy-node@alice-web.service"},
	})
	if err == nil {
		t.Fatal("a deployment unit was allowed to use the helper")
	}
	if resp.ExitCode != privhelper.ExitRefused || !strings.Contains(resp.Output, "refused") {
		t.Fatalf("response = %+v", resp)
	}
}

func TestHandle_refusesInputOnACommandThatTakesNone(t *testing.T) {
	_, err := handle(privhelper.Caller{UID: 0}, privhelper.Request{
		Argv:  []string{"systemctl", "daemon-reload"},
		Input: "not empty",
	})
	if err == nil || !strings.Contains(err.Error(), "takes no input") {
		t.Fatalf("err = %v", err)
	}
}
