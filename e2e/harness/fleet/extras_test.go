package fleet

import (
	"fmt"
	"sync"
	"testing"
)

func TestAddExtra_visibleToLookupAndRemovable(t *testing.T) {
	f := newFake(t, &fakeShell{})
	if err := f.AddExtra(Node{Name: "extra-7", PublicIP: "203.0.113.70"}); err != nil {
		t.Fatal(err)
	}
	if n, ok := f.Lookup("203.0.113.70"); !ok || n.Name != "extra-7" {
		t.Fatalf("lookup %+v %v", n, ok)
	}
	if len(f.AllNodes()) != 4 {
		t.Fatalf("AllNodes %v", f.AllNodes())
	}
	for _, dup := range []string{"extra-7", "extra-1", "node-1", "probe-1", ""} {
		if err := f.AddExtra(Node{Name: dup}); err == nil {
			t.Errorf("%q accepted twice", dup)
		}
	}
	if !f.RemoveExtra("extra-7") || f.RemoveExtra("extra-7") {
		t.Fatal("remove did not report correctly")
	}
	if _, ok := f.Lookup("extra-7"); ok {
		t.Fatal("removed extra still found")
	}
}

// TestAddExtra_parallelReaders is run with -race: extras are added and
// removed while parallel tests look nodes up.
func TestAddExtra_parallelReaders(t *testing.T) {
	f := newFake(t, &fakeShell{})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("extra-%d", 100+i)
			if err := f.AddExtra(Node{Name: name}); err != nil {
				t.Error(err)
			}
			f.RemoveExtra(name)
		}()
		go func() {
			defer wg.Done()
			f.Lookup("node-1")
			_ = f.AllNodes()
			_ = f.names()
		}()
	}
	wg.Wait()
	if len(f.AllNodes()) != 3 {
		t.Fatalf("left %v", f.AllNodes())
	}
}

func TestNextExtraName_neverReused(t *testing.T) {
	st := &State{Nodes: []Node{{Name: "node-1"}}, Extras: []Node{{Name: "extra-2"}, {Name: "extra-join"}}}
	if got := NextExtraName(st); got != "extra-3" {
		t.Fatalf("got %s", got)
	}
	if got := NextExtraName(&State{}); got != "extra-1" {
		t.Fatalf("empty: got %s", got)
	}
}
