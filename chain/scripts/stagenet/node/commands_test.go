package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRun_refusesAnUnknownOrMissingCommand(t *testing.T) {
	var out bytes.Buffer
	require.Error(t, run(context.Background(), nil, &bytes.Buffer{}, &out))
	require.ErrorContains(t, run(context.Background(), []string{"nope"}, &bytes.Buffer{}, &out), "unknown command")
}

func TestCmdAgent_refusesANegativeTTLAndNeedsASocket(t *testing.T) {
	require.ErrorContains(t, run(context.Background(), []string{"agent", "--ttl", "-1s"}, &bytes.Buffer{}, &bytes.Buffer{}), "--ttl")
	key := "0101010101010101010101010101010101010101010101010101010101010101\n"
	require.ErrorContains(t, run(context.Background(), []string{"agent"}, bytes.NewBufferString(key), &bytes.Buffer{}), "at least one --listen")
}

func TestCmdAgent_stopsAtItsTTL(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "a.sock")
	key := "0101010101010101010101010101010101010101010101010101010101010101\n"
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"agent", "--ttl", "300ms", "--listen", sock + ":" + strconv.Itoa(os.Getuid())}, bytes.NewBufferString(key), &bytes.Buffer{})
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop at its ttl")
	}
}
