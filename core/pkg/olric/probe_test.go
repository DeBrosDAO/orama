package olric_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
)

const probeTimeout = 2 * time.Second

// The probe speaks Olric's protocol: a real member answers with the cluster, itself the coordinator.
func TestMembers_aRunningOlricReportsItsCluster(t *testing.T) {
	srv := olrictest.Start(t)
	members, err := olric.Members(context.Background(), srv.Addr, probeTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || !members[0].Coordinator {
		t.Fatalf("members = %+v, want one coordinator", members)
	}
}

func TestMembers_nothingListeningIsAnError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := olric.Members(context.Background(), addr, probeTimeout); err == nil {
		t.Fatal("no error for an address nothing listens on")
	}
}
