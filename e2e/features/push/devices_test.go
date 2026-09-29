//go:build e2e_fleet

package push

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

type deviceList struct {
	Devices []struct {
		ID       string `json:"id"`
		DeviceID string `json:"device_id"`
		Provider string `json:"provider"`
	} `json:"devices"`
}

func register(t testing.TB, c *gw.Client, who tenancy.Cred, deviceID, provider, token string) *gw.Response {
	t.Helper()
	return tenancy.Post(t, c, pathDevices, who, map[string]string{"device_id": deviceID, "provider": provider, "token": token, "platform": "android"})
}

func devices(t testing.TB, c *gw.Client, who tenancy.Cred) deviceList {
	t.Helper()
	var l deviceList
	if err := tenancy.Get(t, c, pathDevices, who).Expect(t, http.StatusOK).Decode(&l); err != nil {
		t.Fatal(err)
	}
	return l
}

// TestDevices_registerListDelete: a user registers, lists (the token is not
// returned) and deletes their own device; deleting it twice or someone
// else's is 404; malformed registrations are 400
// (docs/PUSH_NOTIFICATIONS.md#step-5--register-devices-from-your-client).
func TestDevices_registerListDelete(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	owner := tenancy.Owner(n)
	token := randomTopic(t)
	var reg struct{ Status, ID string }
	if err := register(t, n.Client, owner, "phone-1", "ntfy", token).Expect(t, http.StatusOK).Decode(&reg); err != nil || reg.ID == "" {
		t.Fatalf("register: %v %+v", err, reg)
	}
	l := devices(t, n.Client, owner)
	raw, _ := json.Marshal(l)
	if len(l.Devices) != 1 || l.Devices[0].DeviceID != "phone-1" || strings.Contains(string(raw), token) {
		t.Errorf("devices list %s", raw)
	}
	other := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}
	status(t, "another user deleting my device", tenancy.Send(t, n.Client, http.MethodDelete, pathDevices+"/"+reg.ID, other, nil), http.StatusNotFound)
	tenancy.Send(t, n.Client, http.MethodDelete, pathDevices+"/"+reg.ID, owner, nil).Expect(t, http.StatusOK)
	status(t, "deleting twice", tenancy.Send(t, n.Client, http.MethodDelete, pathDevices+"/"+reg.ID, owner, nil), http.StatusNotFound)
	for name, r := range map[string]*gw.Response{
		"no device_id":     tenancy.Post(t, n.Client, pathDevices, owner, map[string]string{"provider": "ntfy", "token": token}),
		"unknown provider": register(t, n.Client, owner, "p", "fcm", token),
		"empty token":      register(t, n.Client, owner, "p", "ntfy", ""),
		"token over 512":   register(t, n.Client, owner, "p", "ntfy", strings.Repeat("a", maxToken+1)),
		"body over 4 KiB":  register(t, n.Client, owner, strings.Repeat("d", maxRegister), "ntfy", token),
		"not JSON":         tenancy.Post(t, n.Client, pathDevices, owner, []byte("device_id=x")),
	} {
		status(t, name, r, http.StatusBadRequest, http.StatusRequestEntityTooLarge)
	}
	status(t, "PUT devices", tenancy.Send(t, n.Client, http.MethodPut, pathDevices, owner, map[string]string{}), http.StatusMethodNotAllowed)
	tenancy.ExpectRefused(t, register(t, n.Client, tenancy.Cred{}, "p", "ntfy", token), http.StatusUnauthorized, tenancy.CodeMissing)
	reader := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()}
	tenancy.ExpectRefused(t, register(t, n.Client, reader, "p", "ntfy", token), http.StatusForbidden, tenancy.CodeScope)
}

// TestDevices_revokedDeviceRegistrationDropped: a registration made from a
// device-bound session disappears once that device is revoked
// (docs/PUSH_NOTIFICATIONS.md#registrations-from-a-device-bound-session).
func TestDevices_revokedDeviceRegistrationDropped(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	tenancy.Post(t, n.Client, tenancy.PathMembers, tenancy.Owner(n), map[string]string{"wallet": w.Address(), "role": tenancy.RoleRuntime}).
		Expect(t, http.StatusCreated)
	dev := gw.NewDevice(t, wallet.AlgEd25519)
	bound, err := n.Client.For(t).SignIn(t.Context(), w, n.Name, dev)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := n.Client.For(t).SignIn(t.Context(), w, n.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	register(t, n.Client, tenancy.Cred{Bearer: bound.AccessToken}, "bound-phone", "ntfy", randomTopic(t)).Expect(t, http.StatusOK)
	register(t, n.Client, tenancy.Cred{Bearer: plain.AccessToken}, "plain-phone", "ntfy", randomTopic(t)).Expect(t, http.StatusOK)
	if got := len(devices(t, n.Client, tenancy.Cred{Bearer: plain.AccessToken}).Devices); got != 2 {
		t.Fatalf("%d registrations before the revoke, want 2", got)
	}
	if _, err := n.Client.For(t).RevokeDevice(t.Context(), plain.AccessToken, dev.ID(), nil); err != nil {
		t.Fatalf("revoking the device: %v", err)
	}
	l := devices(t, n.Client, tenancy.Cred{Bearer: plain.AccessToken})
	if len(l.Devices) != 1 || l.Devices[0].DeviceID != "plain-phone" {
		t.Errorf("after revoking the device the list is %+v, want only plain-phone", l.Devices)
	}
}
