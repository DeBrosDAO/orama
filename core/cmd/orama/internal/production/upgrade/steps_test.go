package upgrade

import "testing"

// The first upgrade to the release that introduced orama-privhelper ran every
// pre-re-exec phase under the previous release's code, which never installed
// the helper. The helper step has to run after the re-exec and before the
// templates and the restart, or orama-node comes back unable to start a unit
// and the rollout halts at the first node.
func TestPreRestartSteps_PrivilegedHelperComesBeforeTemplates(t *testing.T) {
	steps := (&Orchestrator{}).preRestartSteps()
	index := map[string]int{}
	for i, s := range steps {
		if s.run == nil {
			t.Errorf("step %q has no function", s.name)
		}
		index[s.name] = i
	}
	helper, ok := index["privileged helper"]
	if !ok {
		t.Fatal("the privileged helper step is missing from the post-re-exec steps")
	}
	templates, ok := index["namespace template installation failed"]
	if !ok {
		t.Fatal("the template step is missing")
	}
	if helper > templates {
		t.Errorf("privileged helper (step %d) must run before the templates (step %d)", helper, templates)
	}
}
