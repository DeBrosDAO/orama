package host

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRun_writesTheHandlersOutput(t *testing.T) {
	var out bytes.Buffer

	err := run(strings.NewReader(`{"a":1}`), &out, func(in []byte) ([]byte, error) { return append([]byte("got "), in...), nil })

	if err != nil || out.String() != `got {"a":1}` {
		t.Errorf("out %q, err %v", out.String(), err)
	}
}

func TestRun_aHandlerErrorIsWrittenAsJSONAndReturned(t *testing.T) {
	var out bytes.Buffer

	err := run(strings.NewReader(``), &out, func([]byte) ([]byte, error) { return nil, errors.New(`cache said "no"`) })

	if err == nil || out.String() != `{"error":"cache said \"no\""}` {
		t.Errorf("out %q, err %v", out.String(), err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("stdin closed") }

func TestRun_anInputThatCannotBeReadIsAFailure(t *testing.T) {
	var out bytes.Buffer
	called := false

	err := run(brokenReader{}, &out, func([]byte) ([]byte, error) { called = true; return nil, nil })

	if err == nil || called || !strings.Contains(out.String(), "stdin closed") {
		t.Errorf("out %q, err %v, handler called %v", out.String(), err, called)
	}
}

func TestFake_countersAreAtomicallyAddedAndZeroOnFailure(t *testing.T) {
	f := NewFake()

	if f.CacheIncrBy("k", 2) != 2 || f.CacheIncrBy("k", 3) != 5 || f.CacheIncrBy("k", 0) != 5 {
		t.Error("the counter does not add")
	}
	f.FailCache = true
	if f.CacheIncrBy("k", 1) != 0 {
		t.Error("a failing cache must answer 0, as the runtime does")
	}
}

func TestFake_withoutHooksThereIsNoDatabase(t *testing.T) {
	f := NewFake()

	if _, err := f.DBQuery("SELECT 1"); !errors.Is(err, ErrNoDatabase) {
		t.Errorf("DBQuery err = %v", err)
	}
	if _, err := f.DBExec("INSERT 1"); !errors.Is(err, ErrNoDatabase) {
		t.Errorf("DBExec err = %v", err)
	}
}

func TestNew_offTheGatewayIsAFake(t *testing.T) {
	if _, ok := New().(*Fake); !ok {
		t.Error("New off WASI must be the in-memory host")
	}
}
