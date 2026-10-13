package cli

import "testing"

func TestRecordDelegation_replacesThePreviousResultForTheDomain(t *testing.T) {
	cleanup := writeTestConfig(t, defaultTestConfig())
	defer cleanup()

	first := DelegationStatus{Domain: "stage.example.test", Findings: []string{"NS missing"}}
	if err := RecordDelegation("devnet", first); err != nil {
		t.Fatal(err)
	}
	other := DelegationStatus{Domain: "other.example.test", Delegated: true}
	if err := RecordDelegation("devnet", other); err != nil {
		t.Fatal(err)
	}
	second := DelegationStatus{Domain: "stage.example.test", Delegated: true}
	if err := RecordDelegation("devnet", second); err != nil {
		t.Fatal(err)
	}
	env, err := GetEnvironmentByName("devnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Delegations) != 2 {
		t.Fatalf("delegations = %+v, want one per domain", env.Delegations)
	}
	got := env.Delegations[0]
	if got.Domain != "stage.example.test" || !got.Delegated || len(got.Findings) != 0 || got.CheckedAt == "" {
		t.Fatalf("first domain = %+v, want the newer, stamped result", got)
	}
}

func TestRecordDelegation_refusesAnUnknownEnvironmentAndAnEmptyDomain(t *testing.T) {
	cleanup := writeTestConfig(t, defaultTestConfig())
	defer cleanup()

	if err := RecordDelegation("missing", DelegationStatus{Domain: "stage.example.test"}); err == nil {
		t.Fatal("an unknown environment accepted a delegation result")
	}
	if err := RecordDelegation("devnet", DelegationStatus{}); err == nil {
		t.Fatal("a result without a domain was stored")
	}
}
