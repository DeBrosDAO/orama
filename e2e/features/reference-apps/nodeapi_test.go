//go:build e2e_fleet

package referenceapps

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	todoApp     = "todo-api"
	todoStoreFn = "todo-store"
	todoUsers   = 6
	todosEach   = 3
)

// todoList is the Node API's GET /api/todos.
type todoList struct {
	Owner  string `json:"owner"`
	Cached bool   `json:"cached"`
	Items  []struct {
		Text string `json:"text"`
	} `json:"items"`
}

// deployTodoAPI deploys the store function and the Node API, grants the API
// the runtime role and waits for it on every node.
func deployTodoAPI(t *testing.T) (*realistic.Tenant, string) {
	t.Helper()
	realistic.RequireTinyGo(t)
	requireNPM(t)
	tn := realistic.NewTenant(t)
	requireNodeRuntime(t, tn.F)
	tn.DeployFunction(t, todoStoreFn, "store", false)
	u := tn.Deploy(t, "nodejs", realistic.ServerApp(t, tn.F, realistic.AppNodeAPI, "api-v1"), todoApp,
		"--env", "STORE_FN="+todoStoreFn, "--env", "APP_VERSION=api-v1")
	tn.Grant(t, todoApp, roleRuntime)
	tn.EveryNodeServes(t, u, "/health", "ok")
	requireReplicas(t, tn, "node", todoApp)
	return tn, u
}

// TestReferenceNodeAPI_signedInUsersJourney: six users sign in with wallets
// bound to device keys and use the Node API concurrently. The API learns who
// each is from the gateway (/v1/auth/whoami), stores through the store
// function and caches each list, all as itself; each user sees exactly their
// own todos, a second read comes from the cache, sessions refresh with a
// device proof mid-journey, and nobody gets in without a session
// (website/src/docs/developer/deployments.mdx "Your app's own credential", docs/whitepaper/technical-reference/vol1/14-authorization.md "Devices").
func TestReferenceNodeAPI_signedInUsersJourney(t *testing.T) {
	t.Parallel()
	tn, u := deployTodoAPI(t)
	app := tn.App(u)
	users := realistic.NewUsers(t, tn, roleRuntime, todoUsers)
	journeys(t, app, users, "before refresh")
	for _, usr := range users {
		if err := usr.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	journeys(t, app, users, "after refresh")
	for name, bearer := range map[string]string{"no session": "", "garbage": "a.b.c"} {
		if status, _ := getJSON(t.Context(), app, "/api/todos", bearer, nil); status != http.StatusUnauthorized {
			t.Errorf("%s: the API answered %d, want 401", name, status)
		}
	}
}

// journeys runs one journey per user at once and checks every user's list.
func journeys(t *testing.T, app *gw.Client, users []*realistic.User, phase string) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, len(users))
	for i, usr := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = journey(t.Context(), app, usr, phase)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("user %d (%s): %v", i, phase, err)
		}
	}
}

// journey adds todosEach todos and reads the list twice: the first read
// after a write is fresh from the store, the second is the cached copy, and
// both hold exactly this user's todos of this phase.
func journey(ctx context.Context, app *gw.Client, usr *realistic.User, phase string) error {
	var want []string
	for i := range todosEach {
		text := fmt.Sprintf("%s %s #%d", usr.Wallet.Address()[:10], phase, i)
		if _, err := postJSON(ctx, app, "/api/todos", usr.Token(), map[string]string{"text": text}, nil); err != nil {
			return err
		}
		want = append(want, text)
	}
	for read, wantCached := range []bool{false, true} {
		var l todoList
		if _, err := getJSON(ctx, app, "/api/todos", usr.Token(), &l); err != nil {
			return err
		}
		if l.Cached != wantCached {
			return fmt.Errorf("read %d: cached=%t, want %t", read, l.Cached, wantCached)
		}
		if err := sameTexts(l, usr.Subject(), want); err != nil {
			return fmt.Errorf("read %d: %w", read, err)
		}
	}
	return nil
}

// sameTexts checks that the list is the user's and holds want among its
// items and nothing written by someone else.
func sameTexts(l todoList, owner string, want []string) error {
	if l.Owner != owner {
		return fmt.Errorf("the list is %s's, want %s's", l.Owner, owner)
	}
	have := map[string]bool{}
	for _, it := range l.Items {
		have[it.Text] = true
	}
	for _, w := range want {
		if !have[w] {
			return fmt.Errorf("%q is missing from %d items", w, len(l.Items))
		}
	}
	prefix := owner[:10]
	var foreign []string
	for text := range have {
		if len(text) < len(prefix) || !strings.EqualFold(text[:len(prefix)], prefix) {
			foreign = append(foreign, text)
		}
	}
	sort.Strings(foreign)
	if len(foreign) > 0 {
		return fmt.Errorf("the list holds other users' todos: %v", foreign)
	}
	return nil
}
