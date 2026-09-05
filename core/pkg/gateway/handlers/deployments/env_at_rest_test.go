package deployments

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"go.uber.org/zap"
)

// fillDeploymentRow writes one row into whatever anonymous row struct the
// service asked for, so a test can hand GetDeployment a stored environment.
func fillDeploymentRow(dest interface{}, fields map[string]string) {
	slice := reflect.ValueOf(dest).Elem()
	row := reflect.New(slice.Type().Elem()).Elem()
	for name, value := range fields {
		f := row.FieldByName(name)
		if f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
			f.SetString(value)
		}
	}
	slice.Set(reflect.Append(slice, row))
}

func serviceWithStoredEnv(stored string) *DeploymentService {
	db := &mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			fillDeploymentRow(dest, map[string]string{
				"ID":            "dep-1",
				"Namespace":     "acme",
				"Name":          "api",
				"Type":          "go-backend",
				"Status":        "active",
				"RestartPolicy": "on-failure",
				"Environment":   stored,
			})
			return nil
		},
	}
	return &DeploymentService{db: db, logger: zap.NewNop(), envCodec: testEnvCodec()}
}

// An environment that cannot be read is not an empty environment. Handing the
// app an empty one starts it without its database URL and its API keys, which
// looks like the tenant's own bug and is far harder to find than a refusal.
func TestGetDeployment_refusesAnEnvironmentItCannotRead(t *testing.T) {
	svc := serviceWithStoredEnv("enc:this-is-not-decryptable")

	if _, err := svc.GetDeployment(context.Background(), "acme", "api"); err == nil {
		t.Fatal("GetDeployment returned a deployment with an unreadable environment")
	}

	byID := serviceWithStoredEnv("enc:this-is-not-decryptable")
	if _, err := byID.GetDeploymentByID(context.Background(), "acme", "dep-1"); err == nil {
		t.Fatal("GetDeploymentByID returned a deployment with an unreadable environment")
	}
}

func TestGetDeployment_readsASealedEnvironment(t *testing.T) {
	codec := testEnvCodec()
	sealed, err := codec.Encode(map[string]string{"DATABASE_URL": "postgres://u:p@h/db"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := serviceWithStoredEnv(sealed).GetDeployment(context.Background(), "acme", "api")
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got.Environment["DATABASE_URL"] != "postgres://u:p@h/db" {
		t.Fatalf("environment came back as %#v", got.Environment)
	}
}

// Rows written before the column was encrypted hold plaintext JSON. Refusing
// them would strand every deployment that already exists.
func TestGetDeployment_readsALegacyPlaintextEnvironment(t *testing.T) {
	got, err := serviceWithStoredEnv(`{"OLD":"value"}`).GetDeployment(context.Background(), "acme", "api")
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got.Environment["OLD"] != "value" {
		t.Fatalf("environment came back as %#v", got.Environment)
	}
}

// The column is where the platform tells people to put their secrets, so what
// reaches the database must not be those secrets.
func TestPersistEnv_writesTheEnvironmentSealed(t *testing.T) {
	var written string
	db := &mockRQLiteClient{
		ExecFunc: func(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
			if strings.Contains(query, "UPDATE deployments SET environment") && len(args) > 0 {
				written, _ = args[0].(string)
			}
			return nil, nil
		},
	}
	handler := &EnvHandler{
		service: &DeploymentService{db: db, logger: zap.NewNop(), envCodec: testEnvCodec()},
		logger:  zap.NewNop(),
	}

	err := handler.persistEnv(context.Background(),
		&deployments.Deployment{Namespace: "acme", Name: "api"},
		map[string]string{"STRIPE_KEY": "sk_live_supersecret"})
	if err != nil {
		t.Fatalf("persistEnv: %v", err)
	}

	if written == "" {
		t.Fatal("persistEnv wrote nothing")
	}
	if strings.Contains(written, "sk_live_supersecret") {
		t.Fatalf("the secret reached the database in the clear: %s", written)
	}
	if !testEnvCodec().IsEncrypted(written) {
		t.Fatalf("the stored environment is not sealed: %s", written)
	}
}

func TestApplyEnvChanges_refusesAValueThatCouldNotBeDelivered(t *testing.T) {
	if _, err := applyEnvChanges(nil, map[string]string{"K": "\xff"}, nil); err == nil {
		t.Error("a value systemd would discard was accepted")
	}
	if _, err := applyEnvChanges(nil, map[string]string{"K": "a\x00b"}, nil); err == nil {
		t.Error("a value carrying a NUL byte was accepted")
	}
	if _, err := applyEnvChanges(nil, map[string]string{"K": strings.Repeat("x", deployments.MaxEnvValueBytes+1)}, nil); err == nil {
		t.Error("an oversized value was accepted")
	}
	if _, err := applyEnvChanges(nil, map[string]string{"K": "-----BEGIN KEY-----\nabc\n-----END KEY-----"}, nil); err != nil {
		t.Errorf("a legitimate multi-line value was refused: %v", err)
	}
}

// The platform sets these itself. A tenant that could set ORAMA_GATEWAY_URL
// could point its own app at another namespace's gateway.
func TestValidateEnvKey_refusesEveryNameThePlatformOwns(t *testing.T) {
	for _, key := range []string{"PORT", "ENTRY_POINT", "ORAMA_NAMESPACE", "ORAMA_GATEWAY_URL", "ORAMA_STATE_DIR", "ORAMA_CACHE_DIR"} {
		if err := validateEnvKey(key); err == nil {
			t.Errorf("%s is settable by a tenant", key)
		}
	}
	if err := validateEnvKey("DATABASE_URL"); err != nil {
		t.Errorf("an ordinary name was refused: %v", err)
	}
}
