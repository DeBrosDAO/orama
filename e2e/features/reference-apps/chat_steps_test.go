//go:build e2e_fleet

package referenceapps

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// attachmentSize is a photo-sized upload.
const attachmentSize = 256 << 10

// round has every member send chatMessages at once and checks that every
// socket got every message exactly once, sent as its sender, and that the
// room's history holds them all under their senders.
func (ch *chat) round(t *testing.T, phase string) {
	t.Helper()
	sent := map[string]string{} // text -> sender subject
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, m := range ch.members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range chatMessages {
				text := fmt.Sprintf("%s-%s-%d", phase, m.id, k)
				if err := ch.send(t, m, text, ""); err != nil {
					t.Errorf("%s: %v", m.id, err)
					continue
				}
				mu.Lock()
				sent[text] = m.u.Subject()
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	eventually.Require(t, pollEvery, deliveryBudget, phase+" delivered to every socket", func() (bool, error) {
		ch.collect()
		for _, m := range ch.members {
			if got := m.countPrefix(phase + "-"); got < len(sent) {
				return false, fmt.Errorf("%s has %d of %d", m.id, got, len(sent))
			}
		}
		return true, nil
	})
	for _, m := range ch.members {
		for text, from := range sent {
			if n := m.count(text); n != 1 {
				t.Errorf("%s received %q %d times, want once", m.id, text, n)
			}
			if f := m.sender(text); !strings.EqualFold(f, from) {
				t.Errorf("%s: %q arrived from %q, sent by %s", m.id, text, f, from)
			}
		}
	}
	ch.checkHistory(t, sent)
}

// sender is who m's copy of text says sent it.
func (m *member) sender(text string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.seen {
		if s.Text == text {
			return s.From
		}
	}
	return ""
}

// checkHistory reads the room through chat-history as the last member.
func (ch *chat) checkHistory(t *testing.T, sent map[string]string) {
	t.Helper()
	out, err := realistic.Invoke(t.Context(), ch.tn.C, "chat-history", ch.members[len(ch.members)-1].u.Token(), map[string]string{"op": "history", "room": ch.room})
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, row := range realistic.Rows(out) {
		text, _ := row["text"].(string)
		owner, _ := row["owner"].(string)
		owners[text] = owner
	}
	for text, from := range sent {
		if !strings.EqualFold(owners[text], from) {
			t.Errorf("history has %q from %q, want %s", text, owners[text], from)
		}
	}
}

// attachment: the first member uploads a file and posts its CID; the third
// downloads it byte for byte.
func (ch *chat) attachment(t *testing.T) {
	t.Helper()
	data := make([]byte, attachmentSize)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	sender, reader := ch.members[0], ch.members[2%len(ch.members)]
	cid := upload(t, ch.tn.C, sender.u.Token(), "photo.jpg", data)
	text := "attachment:" + cid
	if err := ch.send(t, sender, text, ""); err != nil {
		t.Fatal(err)
	}
	eventually.Require(t, pollEvery, meshBudget, "the attachment downloaded by "+reader.id, func() (bool, error) {
		ch.collect()
		if reader.count(text) == 0 {
			return false, fmt.Errorf("the message has not arrived")
		}
		r, err := ch.tn.C.Send(t.Context(), gw.Req{Path: "/v1/storage/get/" + cid, Bearer: reader.u.Token()})
		if err != nil {
			return false, err
		}
		if r.Status != http.StatusOK {
			return false, fmt.Errorf("download HTTP %d", r.Status)
		}
		if !bytes.Equal(r.Body, data) {
			return false, eventually.Stop(fmt.Errorf("downloaded %d bytes that differ from the %d uploaded", len(r.Body), len(data)))
		}
		return true, nil
	})
}

// upload stores data through the storage API as bearer and returns its CID.
func upload(t testing.TB, c *gw.Client, bearer, name string, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Cid string `json:"cid"`
	}
	r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/storage/upload", Bearer: bearer, Header: http.Header{"Content-Type": {w.FormDataContentType()}}, Body: buf.Bytes()})
	if err := r.Expect(t, http.StatusOK).Decode(&out); err != nil || out.Cid == "" {
		t.Fatalf("upload returned no CID: %v %.200s", err, r.Body)
	}
	return out.Cid
}

// push: the last member registers their phone's ntfy topic; the second
// sends a message naming them; the chat-notify trigger pushes it and it
// lands on every node's ntfy.
func (ch *chat) push(t *testing.T) {
	t.Helper()
	recipient, sender := ch.members[len(ch.members)-1], ch.members[1]
	topic := randomTopic(t)
	registerNtfy(t, ch.tn.C, recipient.u, topic)
	marker := "push-" + randomTopic(t)
	if err := ch.send(t, sender, marker, recipient.u.Subject()); err != nil {
		t.Fatal(err)
	}
	waitNtfy(t, ch.tn, topic, marker)
}

// leave closes the third member's socket: the others see presence.leave and
// the room lists one member fewer.
func (ch *chat) leave(t *testing.T) {
	t.Helper()
	gone := ch.members[2%len(ch.members)]
	gone.sock.Close()
	watcher := ch.members[0]
	eventually.Require(t, pollEvery, deliveryBudget, "presence.leave for "+gone.id, func() (bool, error) {
		for _, f := range watcher.sock.Drain() {
			if strings.Contains(string(f.Data), `"presence.leave"`) && strings.Contains(string(f.Data), gone.id) {
				return true, nil
			}
		}
		return false, nil
	})
	ch.requirePresent(t, len(ch.members)-1)
}
