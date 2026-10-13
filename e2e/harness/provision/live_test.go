package provision

import (
	"context"
	"testing"
)

func TestLiveExtras_onlyServersTheStateDoesNotList(t *testing.T) {
	e, st := upForTest(t)
	brokered := *st
	if _, err := addExtra(context.Background(), &brokered, "extra-b", "nbg1", testLimits, e.d); err != nil {
		t.Fatal(err)
	}
	got, err := liveExtras(context.Background(), st, e.d)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "extra-b" || got[0].PublicIP == "" || got[0].SSHUser != sshUser {
		t.Fatalf("live extras %+v", got)
	}
}
