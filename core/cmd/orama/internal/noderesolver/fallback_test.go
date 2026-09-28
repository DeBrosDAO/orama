package noderesolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func node(host string) inspector.Node { return inspector.Node{Host: host} }

func TestChooseNodes_recordedInventoryBeatsNodesConfWhenTheAPIIsDown(t *testing.T) {
	apiErr := errors.New("no such host")
	confErr := errors.New("nodes.conf not found")
	got, err := chooseNodes(nil, apiErr, []inspector.Node{node("203.0.113.5")}, nil, confErr)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Host != "203.0.113.5" {
		t.Fatalf("got %+v", got)
	}
}

func TestChooseNodes_apiWinsWhenItAnswers(t *testing.T) {
	got, err := chooseNodes(
		[]inspector.Node{node("203.0.113.9")}, nil,
		[]inspector.Node{node("203.0.113.5")},
		[]inspector.Node{node("203.0.113.1")}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Host != "203.0.113.9" {
		t.Fatalf("got %+v", got)
	}
}

func TestChooseNodes_nodesConfIsLast(t *testing.T) {
	got, err := chooseNodes(nil, errors.New("down"), nil, []inspector.Node{node("203.0.113.1")}, nil)
	if err != nil || len(got) != 1 || got[0].Host != "203.0.113.1" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestChooseNodes_reportsBothFailures(t *testing.T) {
	_, err := chooseNodes(nil, errors.New("dns"), nil, nil, errors.New("no file"))
	if err == nil || !strings.Contains(err.Error(), "dns") || !strings.Contains(err.Error(), "no file") {
		t.Fatalf("error = %v", err)
	}
}
