package install

import (
	"strings"
	"testing"
)

func TestFloorFor_fullProfileAddsTheStorageBudget(t *testing.T) {
	f, err := FloorFor(ProfileFull, 100)
	if err != nil {
		t.Fatal(err)
	}
	if f.CPUCores != 4 || f.RAMBytes != 8*bytesPerGiB {
		t.Fatalf("floor %+v, want 4 vCPU and 8 GiB", f)
	}
	if want := 80*uint64(bytesPerGiB) + 100*bytesPerGB; f.DiskBytes != want {
		t.Fatalf("disk floor %d, want %d (80 GiB plus the 100 GB offered)", f.DiskBytes, want)
	}
}

func TestFloorFor_clusterOnlyIgnoresStorage(t *testing.T) {
	f, err := FloorFor(ProfileClusterOnly, 500)
	if err != nil {
		t.Fatal(err)
	}
	if f.CPUCores != MinCPUCores || f.RAMBytes != MinRAMBytes || f.DiskBytes != MinFreeDiskBytes {
		t.Fatalf("cluster-only floor %+v must be the installer's own floors", f)
	}
}

func TestFloorFor_unknownProfile(t *testing.T) {
	if _, err := FloorFor("huge", 0); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Fatalf("got %v, want an unknown-profile error", err)
	}
}

func TestCheckHardware_namesEveryShortfall(t *testing.T) {
	err := CheckHardware(ProfileFull, 0, Hardware{CPUCores: 2, RAMBytes: 4 * bytesPerGiB, FreeDiskBytes: 200 * bytesPerGiB})
	if err == nil {
		t.Fatal("2 vCPU and 4 GiB must be refused for the full profile")
	}
	for _, want := range []string{"2 vCPU (needs 4)", "4.0 GiB RAM (needs 8.0 GiB)", "--cluster-only"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "disk") {
		t.Errorf("error %q blames the disk, which is enough", err)
	}
}

func TestCheckHardware_acceptsExactlyTheFloor(t *testing.T) {
	hw := Hardware{CPUCores: 4, RAMBytes: 8 * bytesPerGiB, FreeDiskBytes: 80 * bytesPerGiB}
	if err := CheckHardware(ProfileFull, 0, hw); err != nil {
		t.Fatalf("a machine at the floor must pass: %v", err)
	}
	if err := CheckHardware(ProfileFull, 1, hw); err == nil {
		t.Fatal("one GB of offered storage above the disk must be refused")
	}
}

func TestCheckHardware_clusterOnlyOnASmallMachine(t *testing.T) {
	hw := Hardware{CPUCores: 2, RAMBytes: 2 * bytesPerGiB, FreeDiskBytes: 10 * bytesPerGiB}
	if err := CheckHardware(ProfileClusterOnly, 0, hw); err != nil {
		t.Fatal(err)
	}
	if err := CheckHardware(ProfileFull, 0, hw); err == nil {
		t.Fatal("the same machine must not pass the full profile")
	}
}

func TestParseHardware_roundTrip(t *testing.T) {
	hw, err := ParseHardware("cpu=8\nram_kb=16384000\ndisk_kb=104857600\n")
	if err != nil {
		t.Fatal(err)
	}
	if hw.CPUCores != 8 || hw.RAMBytes != 16384000*1024 || hw.FreeDiskBytes != 104857600*1024 {
		t.Fatalf("got %+v", hw)
	}
}

func TestParseHardware_refusesAMissingOrBrokenLine(t *testing.T) {
	for name, out := range map[string]string{
		"empty":       "",
		"no disk":     "cpu=4\nram_kb=8000000\n",
		"not numeric": "cpu=four\nram_kb=1\ndisk_kb=1\n",
		"zero cpu":    "cpu=0\nram_kb=1\ndisk_kb=1\n",
	} {
		if _, err := ParseHardware(out); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestParseHardware_ignoresTheProbesOtherLines(t *testing.T) {
	hw, err := ParseHardware("arch=x86_64\ncpu=2\nram_kb=2097152\ndisk_kb=10485760\ncluster=1\n")
	if err != nil || hw.CPUCores != 2 {
		t.Fatalf("got %+v, %v", hw, err)
	}
}
