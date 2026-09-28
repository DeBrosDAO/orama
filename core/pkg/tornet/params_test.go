package tornet

import (
	"errors"
	"testing"
)

func TestParamsRefuseAnExitAndAShortAuthoritySet(t *testing.T) {
	ok := Params{Name: "orama-stagenet", Authorities: 3, ExitPolicy: ExitPolicy, CertMonths: 12}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	short := ok
	short.Authorities = 2
	if err := short.Validate(); err == nil {
		t.Fatal("two authorities were accepted")
	}
	open := ok
	open.ExitPolicy = "accept *:*"
	if err := open.Validate(); err == nil {
		t.Fatal("an open exit policy was accepted")
	}
	if err := StartExit(); !errors.Is(err, ErrExitRefused) {
		t.Fatal(err)
	}
}
