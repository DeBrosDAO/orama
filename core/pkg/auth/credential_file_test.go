package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestWriteCredentialFile_concurrentWritersLeaveValidJSON: two e2e runners
// signing in at once left a credential file no command could parse.
func TestWriteCredentialFile_concurrentWritersLeaveValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := map[string]string{"version": "2.0", "pad": strings.Repeat("x", 1+i*97)}
			data, _ := json.Marshal(body)
			if err := writeCredentialFile(path, data); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("the file is not one writer's JSON: %v", err)
	}
	if left, _ := filepath.Glob(path + ".tmp-*"); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestWriteCredentialFile_narrowsAWiderExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCredentialFile(path, []byte(`{"version":"2.0"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != credentialFilePerm {
		t.Errorf("mode %o, want %o: the file holds tokens", got, credentialFilePerm)
	}
}

func TestWriteCredentialFile_missingDirectoryIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "credentials.json")
	if err := writeCredentialFile(path, []byte("{}")); err == nil {
		t.Fatalf("writing into %s succeeded", path)
	}
}
