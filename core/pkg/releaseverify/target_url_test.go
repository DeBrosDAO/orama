package releaseverify

import "testing"

func TestRepository_targetURLIsWhereTheRepositoryServesTheTarget(t *testing.T) {
	repo := Repository{BaseURL: "https://releases.example/orama/"}
	got, err := repo.TargetURL(Target{Path: "nightly/orama-0.3.1-linux-amd64.tar.gz", Length: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://releases.example/orama/targets/nightly/orama-0.3.1-linux-amd64.tar.gz"; got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
}

func TestRepository_targetURLRefusesWhatFetchTargetRefuses(t *testing.T) {
	repo := Repository{BaseURL: "https://releases.example"}
	for name, target := range map[string]Target{
		"a path out of the targets directory": {Path: "../root.json", Length: 1},
		"an absolute path":                    {Path: "/etc/passwd", Length: 1},
		"a doubled slash":                     {Path: "stable//x", Length: 1},
		"a negative length":                   {Path: "stable/x", Length: -1},
		"a length no release has":             {Path: "stable/x", Length: maxTargetBytes + 1},
	} {
		if _, err := repo.TargetURL(target); err == nil {
			t.Errorf("%s was given a URL", name)
		}
	}
	if _, err := (Repository{BaseURL: "ftp://releases.example"}).TargetURL(Target{Path: "stable/x", Length: 1}); err == nil {
		t.Error("a repository that is not http(s) was given a URL")
	}
}
