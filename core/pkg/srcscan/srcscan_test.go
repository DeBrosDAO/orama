package srcscan

import (
	"errors"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseNonTest_skips_tests_and_non_go_files(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package p\n// c\nfunc A() {}\n")
	write(t, dir, "a_test.go", "package p\nfunc TestA() {}\n")
	write(t, dir, "notes.txt", "not go")
	if err := os.Mkdir(filepath.Join(dir, "sub.go"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := ParseNonTest(token.NewFileSet(), dir)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := got[filepath.Join(dir, "a.go")]
	if len(got) != 1 || !ok {
		t.Fatalf("got %d files, want only a.go", len(got))
	}
	if len(f.Comments) == 0 {
		t.Error("comments must be parsed")
	}
}

func TestParseNonTest_empty_directory_is_no_files(t *testing.T) {
	got, err := ParseNonTest(token.NewFileSet(), t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want no files and no error", got, err)
	}
}

func TestParseNonTest_missing_directory_is_not_exist(t *testing.T) {
	_, err := ParseNonTest(token.NewFileSet(), filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestParseNonTest_syntax_error_is_reported(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "bad.go", "package p\nfunc (")
	if _, err := ParseNonTest(token.NewFileSet(), dir); err == nil {
		t.Fatal("a file that does not parse must be an error")
	}
}
