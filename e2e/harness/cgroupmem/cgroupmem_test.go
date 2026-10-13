package cgroupmem

import "testing"

const sampleMemoryStat = `anon 31457280
file 192937984
kernel 4194304
inactive_file 192937984
anon_thp 0
`

func TestField_readsAnonNotFileCache(t *testing.T) {
	v, err := Field(sampleMemoryStat, Anon)
	if err != nil {
		t.Fatal(err)
	}
	if v != 30<<20 {
		t.Fatalf("anon = %d, want %d: the page cache was counted", v, 30<<20)
	}
}

func TestField_prefixIsNotAMatch(t *testing.T) {
	if v, err := Field("anon_thp 5\nanon 7\n", Anon); err != nil || v != 7 {
		t.Fatalf("anon = %d, %v; want 7 (anon_thp is another field)", v, err)
	}
}

func TestField_missingOrMalformed(t *testing.T) {
	for name, stat := range map[string]string{"empty": "", "absent": "file 1\n", "not a number": "anon x\n"} {
		if _, err := Field(stat, Anon); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
