package install

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// orderRecorder records which isolation steps ran, and fails the one named.
type orderRecorder struct {
	ran  []string
	fail string
}

func (o *orderRecorder) step(name string) func() error {
	return func() error {
		o.ran = append(o.ran, name)
		if name == o.fail {
			return errors.New(name + " failed")
		}
		return nil
	}
}

func (o *orderRecorder) steps() isolationSteps {
	return isolationSteps{
		ensureAccounts:   o.step("accounts"),
		installTemplates: o.step("templates"),
		handRootConfigs:  o.step("configs"),
	}
}

// The Corefile changes group only after the unit that reads it as that group
// is installed, and the accounts exist before the units that name them.
func TestInstallIsolatedTemplates_accountsThenTemplatesThenConfigs(t *testing.T) {
	o := &orderRecorder{}
	if err := installIsolatedTemplates(o.steps()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"accounts", "templates", "configs"}; !reflect.DeepEqual(o.ran, want) {
		t.Fatalf("ran %q, want %q", o.ran, want)
	}
}

// A template install that fails leaves the Corefile in the group of the unit
// still on disk, which can still read it.
func TestInstallIsolatedTemplates_failedTemplatesLeaveTheConfigsAlone(t *testing.T) {
	o := &orderRecorder{fail: "templates"}
	if err := installIsolatedTemplates(o.steps()); err == nil {
		t.Fatal("a failed template install was reported as success")
	}
	if want := []string{"accounts", "templates"}; !reflect.DeepEqual(o.ran, want) {
		t.Fatalf("ran %q, want %q", o.ran, want)
	}
}

func TestInstallIsolatedTemplates_failedAccountsInstallNothing(t *testing.T) {
	o := &orderRecorder{fail: "accounts"}
	if err := installIsolatedTemplates(o.steps()); err == nil {
		t.Fatal("a failed account step was reported as success")
	}
	if want := []string{"accounts"}; !reflect.DeepEqual(o.ran, want) {
		t.Fatalf("ran %q, want %q", o.ran, want)
	}
}

func TestInstallIsolatedTemplates_failedConfigHandOverIsAnError(t *testing.T) {
	o := &orderRecorder{fail: "configs"}
	err := installIsolatedTemplates(o.steps())
	if err == nil || !strings.Contains(err.Error(), "Corefile") {
		t.Fatalf("err = %v, want one naming the Corefile", err)
	}
}
