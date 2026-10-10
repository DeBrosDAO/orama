package setup

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

func createdStagenet(seeds ...string) *CreatedNetwork {
	return &CreatedNetwork{
		Manifest: &netregistry.Manifest{Name: "stagenet", ChainID: "orama-stagenet-6", Seeds: seeds},
		Dir:      "networks/stagenet", Machines: []string{"203.0.113.10", "203.0.113.11"},
	}
}

func TestCreatedNetwork_seedRecordsPairSeedsWithMachines(t *testing.T) {
	got := createdStagenet("seed1.stagenet.orama.network", "seed2.stagenet.orama.network").SeedRecords()
	want := []string{"seed1.stagenet.orama.network.\tIN\tA\t203.0.113.10", "seed2.stagenet.orama.network.\tIN\tA\t203.0.113.11"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records = %q, want %q", got, want)
	}
}

func TestCreatedNetwork_seedRecordsWhenSeedsAndMachinesDiffer(t *testing.T) {
	got := createdStagenet("seed.example.org").SeedRecords()
	if len(got) != 1 || !strings.Contains(got[0], "seed.example.org must resolve to one of [203.0.113.10 203.0.113.11]") {
		t.Errorf("records = %q", got)
	}
}

func TestCreatedNetwork_nextStepsNameTheCommandsInOrder(t *testing.T) {
	steps := strings.Join(createdStagenet("seed1.stagenet.orama.network", "seed2.stagenet.orama.network").NextSteps(), "\n")
	order := []string{
		"networks/stagenet/", "1. Create these DNS records", "seed1.stagenet.orama.network.\tIN\tA\t203.0.113.10",
		"2. In the repository", "make -C core sync-networks", "core/pkg/netregistry/embedded/", "3. Deploy the website",
		"orama network add https://orama.network/networks/stagenet/manifest.json", "orama setup --network stagenet", "new chain id",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(steps, want)
		if i < 0 || i < last {
			t.Fatalf("%q is missing or out of order in:\n%s", want, steps)
		}
		last = i
	}
}

func TestCreatedNetwork_manifestURL(t *testing.T) {
	if got := createdStagenet("a.b").ManifestURL(); got != "https://orama.network/networks/stagenet/manifest.json" {
		t.Errorf("URL = %s", got)
	}
}
