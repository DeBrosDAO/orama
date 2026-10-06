//go:build e2e_fleet

package push

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

type topicReg struct {
	Status    string `json:"status"`
	TopicID   string `json:"topic_id"`
	ExpiresAt int64  `json:"expires_at"`
}

func registerTopic(t testing.TB, c *gw.Client, who tenancy.Cred, secret, token string) *gw.Response {
	t.Helper()
	return tenancy.Post(t, c, pathTopics, who, map[string]string{"topic_secret": secret, "provider": "ntfy", "token": token})
}

// TestTopics_registerRefreshRotateRemove: the topic id is SHA-256 of the
// secret; a registration lives 7-8 days rounded up to a UTC day and a
// refresh moves it forward; a token belongs to one topic, so registering it
// under a new secret removes the old topic; DELETE needs the secret and a
// wrong one is 404 (docs/PUSH_NOTIFICATIONS.md#step-5b--register-by-rotating-push-topic-no-account-binding).
func TestTopics_registerRefreshRotateRemove(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	key := tenancy.Cred{APIKey: tenancy.APIKey(t, n, "push")}
	secret, id := hexSecret(t, 32)
	token := randomTopic(t)
	var reg topicReg
	if err := registerTopic(t, n.Client, key, secret, token).Expect(t, http.StatusOK).Decode(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.TopicID != id {
		t.Errorf("topic id %s, want sha256(secret) %s", reg.TopicID, id)
	}
	exp := time.Unix(reg.ExpiresAt, 0)
	if left := time.Until(exp); left < topicTTL-time.Minute || left > topicTTL+day || reg.ExpiresAt%int64(day.Seconds()) != 0 {
		t.Errorf("expires_at %s: want 7-8 days away on a UTC day boundary", exp.UTC())
	}
	var again topicReg
	if err := registerTopic(t, n.Client, key, secret, token).Expect(t, http.StatusOK).Decode(&again); err != nil {
		t.Fatal(err)
	}
	if again.TopicID != id || again.ExpiresAt < reg.ExpiresAt {
		t.Errorf("refresh answered %+v after %+v", again, reg)
	}
	wrong, _ := hexSecret(t, 32)
	status(t, "DELETE with a wrong secret", del(t, n.Client, pathTopics, key, map[string]string{"topic_secret": wrong}), http.StatusNotFound)
	status(t, "DELETE with the topic id as the secret", del(t, n.Client, pathTopics, key, map[string]string{"topic_secret": id}), http.StatusNotFound)
	rotated, rotatedID := hexSecret(t, 32)
	registerTopic(t, n.Client, key, rotated, token).Expect(t, http.StatusOK)
	status(t, "the old topic after rotation", del(t, n.Client, pathTopics, key, map[string]string{"topic_secret": secret}), http.StatusNotFound)
	status(t, "send to the rotated-away topic", tenancy.Post(t, n.Client, pathTopicsSend, key, map[string]string{"topic_id": id, "title": "x", "body": "y"}), http.StatusNotFound)
	del(t, n.Client, pathTopics, key, map[string]string{"topic_secret": rotated}).Expect(t, http.StatusOK)
	status(t, "send after removal", tenancy.Post(t, n.Client, pathTopicsSend, key, map[string]string{"topic_id": rotatedID, "title": "x", "body": "y"}), http.StatusNotFound)
}

// TestTopics_inputAndAccess: secrets outside 16-64 bytes or not hex, unknown
// providers, missing or oversized tokens are 400; other methods 405; no
// grant 403; malformed topic ids on send 400.
func TestTopics_inputAndAccess(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	owner := tenancy.Owner(n)
	s15, _ := hexSecret(t, 15)
	s16, _ := hexSecret(t, 16)
	s64, _ := hexSecret(t, 64)
	s65, _ := hexSecret(t, 65)
	token := randomTopic(t)
	registerTopic(t, n.Client, owner, s16, token).Expect(t, http.StatusOK)
	registerTopic(t, n.Client, owner, s64, randomTopic(t)).Expect(t, http.StatusOK)
	for name, r := range map[string]*gw.Response{
		"15-byte secret": registerTopic(t, n.Client, owner, s15, token),
		"65-byte secret": registerTopic(t, n.Client, owner, s65, token),
		"not hex":        registerTopic(t, n.Client, owner, strings.Repeat("zz", 32), token),
		"odd hex":        registerTopic(t, n.Client, owner, s16+"a", token),
		"empty token":    registerTopic(t, n.Client, owner, s16, ""),
		"token over 512": registerTopic(t, n.Client, owner, s16, strings.Repeat("t", maxToken+1)),
		"unknown provider": tenancy.Post(t, n.Client, pathTopics, owner,
			map[string]string{"topic_secret": s16, "provider": "sms", "token": token}),
		"not JSON":             tenancy.Post(t, n.Client, pathTopics, owner, []byte("topic_secret=x")),
		"send short topic id":  tenancy.Post(t, n.Client, pathTopicsSend, owner, map[string]string{"topic_id": "abc", "title": "t"}),
		"send non-hex topic":   tenancy.Post(t, n.Client, pathTopicsSend, owner, map[string]string{"topic_id": strings.Repeat("z", 64)}),
		"send body over 64KiB": tenancy.Post(t, n.Client, pathTopicsSend, owner, map[string]string{"topic_id": strings.Repeat("a", 64), "body": strings.Repeat("b", maxSendBody)}),
	} {
		status(t, name, r, http.StatusBadRequest, http.StatusRequestEntityTooLarge)
	}
	status(t, "GET topics", tenancy.Get(t, n.Client, pathTopics, owner), http.StatusMethodNotAllowed)
	status(t, "unknown topic id", tenancy.Post(t, n.Client, pathTopicsSend, owner, map[string]string{"topic_id": strings.Repeat("a", 64), "title": "t"}), http.StatusNotFound)
	reader := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()}
	tenancy.ExpectRefused(t, registerTopic(t, n.Client, reader, s16, token), http.StatusForbidden, tenancy.CodeScope)
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathTopicsSend, reader, map[string]string{"topic_id": strings.Repeat("a", 64)}), http.StatusForbidden, tenancy.CodeScope)
	tenancy.ExpectRefused(t, registerTopic(t, n.Client, tenancy.Cred{}, s16, token), http.StatusUnauthorized, tenancy.CodeMissing)
}

// TestTopics_namespacesDoNotShare: the same secret names the same topic id
// in two namespaces, but a topic registered in one is unknown to the other.
func TestTopics_namespacesDoNotShare(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := nss[0], nss[1]
	secret, id := hexSecret(t, 32)
	registerTopic(t, a.Client, tenancy.Owner(a), secret, randomTopic(t)).Expect(t, http.StatusOK)
	status(t, "the other namespace sending to it", tenancy.Post(t, b.Client, pathTopicsSend, tenancy.Owner(b), map[string]string{"topic_id": id, "title": "x"}), http.StatusNotFound)
	status(t, "the other namespace removing it", del(t, b.Client, pathTopics, tenancy.Owner(b), map[string]string{"topic_secret": secret}), http.StatusNotFound)
	tenancy.ExpectDenied(t, tenancy.Post(t, a.Client, pathTopicsSend, tenancy.Owner(b), map[string]string{"topic_id": id}), "another namespace's session")
}
