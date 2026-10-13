package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/broadcast"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
)

func TestParseOptions_defaultsToTheFourNodeLocalnetSmokeProfile(t *testing.T) {
	o, err := parseOptions("run", []string{"-funder-key-file", "k"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tcp://127.0.0.1:31001", "tcp://127.0.0.1:31011", "tcp://127.0.0.1:31021", "tcp://127.0.0.1:31031"}
	if strings.Join(o.cfg.Nodes, ",") != strings.Join(want, ",") {
		t.Fatalf("nodes %v", o.cfg.Nodes)
	}
	if o.cfg.Profile.Name != "smoke" || o.cfg.Broadcast != broadcast.ModeSync || o.cfg.FunderKeyFile != "k" {
		t.Fatalf("config %+v", o.cfg)
	}
}

func TestParseOptions_explicitNodesTypesAndMode(t *testing.T) {
	o, err := parseOptions("run", []string{"-nodes", "tcp://a:1, tcp://b:2", "-types", "bank-send,market-list", "-broadcast", "async", "-profile", "full", "-workers", "8"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.cfg.Nodes) != 2 || o.cfg.Nodes[1] != "tcp://b:2" {
		t.Fatalf("nodes %v", o.cfg.Nodes)
	}
	if len(o.cfg.Profile.Types) != 2 || o.cfg.Profile.Types[0].Type != gen.BankSend || o.cfg.Profile.Name != "full" {
		t.Fatalf("profile %+v", o.cfg.Profile)
	}
	if o.cfg.Broadcast != broadcast.ModeAsync || o.cfg.Workers != 8 {
		t.Fatalf("config %+v", o.cfg)
	}
}

func TestParseOptions_errors(t *testing.T) {
	cases := map[string][]string{
		"unknown profile":   {"-profile", "nightly"},
		"unknown type":      {"-types", "bank-sned"},
		"unknown broadcast": {"-broadcast", "fire"},
		"one validator":     {"-localnet-nodes", "1"},
		"unknown flag":      {"-nope"},
	}
	for name, args := range cases {
		if _, err := parseOptions("run", args, io.Discard); err == nil {
			t.Errorf("%s: accepted %v", name, args)
		}
	}
}

func TestExecute_usageAndList(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := execute(context.Background(), nil, &out, &errOut); code != exitUsage {
		t.Fatalf("no command: %d", code)
	}
	if code := execute(context.Background(), []string{"fly"}, &out, &errOut); code != exitUsage || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("unknown command: %d %q", code, errOut.String())
	}
	out.Reset()
	if code := execute(context.Background(), []string{"list"}, &out, &errOut); code != exitOK {
		t.Fatalf("list: %d", code)
	}
	for _, want := range []string{"bank-send", "shielded-transfer-16", "market-list", "smoke", "full"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, out.String())
		}
	}
}

func TestExecute_runWithoutAFunderKeyFailsBeforeTouchingTheChain(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"run", "-nodes", "tcp://127.0.0.1:1,tcp://127.0.0.1:2"}, &out, &errOut)
	if code != exitError || !strings.Contains(errOut.String(), "funder key") {
		t.Fatalf("code %d: %s", code, errOut.String())
	}
}
