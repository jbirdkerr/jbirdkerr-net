package apitoken

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/config"
	apppostgres "github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/postgres"
	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/secrets"
	"github.com/pulumi/pulumi-postgresql/sdk/v3/go/postgresql"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

var b64 = base64.RawURLEncoding

func newKeyJWK(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := k.PublicKey.Bytes()
	d, _ := k.Bytes()
	j, _ := json.Marshal(map[string]string{"kty": "EC", "crv": "P-256", "kid": "kid-1",
		"x": b64.EncodeToString(pub[1:33]), "y": b64.EncodeToString(pub[33:65]), "d": b64.EncodeToString(d)})
	return k, string(j)
}

func app(t *testing.T, yaml string) *config.AppConfig {
	t.Helper()
	a, err := config.ParseAppConfig([]byte(yaml), "appops.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

const (
	plainYAML = "name: blog\n"
	apiYAML   = "name: blog\npermissions:\n  functions: [EXECUTE]\napi:\n  enabled: true\n  issued_on: 2026-11-01\n  expires_in_days: 30\n"
)

func TestLoadSigningKey_NotNeededWithoutAPIApps(t *testing.T) {
	key, kid, err := LoadSigningKey([]*config.AppConfig{app(t, plainYAML)}, "")
	if err != nil || key != nil || kid != "" {
		t.Errorf("expected no key and no error, got %v %q %v", key, kid, err)
	}
	// A malformed value is ignored too: nothing reads it.
	if _, _, err := LoadSigningKey([]*config.AppConfig{app(t, plainYAML)}, "{garbage"); err != nil {
		t.Errorf("expected no error when no app needs the key, got %v", err)
	}
}

func TestLoadSigningKey_FailsClosed(t *testing.T) {
	apps := []*config.AppConfig{app(t, plainYAML), app(t, apiYAML)}
	if _, _, err := LoadSigningKey(apps, ""); err == nil || !strings.Contains(err.Error(), EnvSigningJWK) {
		t.Errorf("expected error naming %s, got %v", EnvSigningJWK, err)
	}
	if _, _, err := LoadSigningKey(apps, "{garbage"); err == nil || !strings.Contains(err.Error(), EnvSigningJWK) {
		t.Errorf("expected parse error naming %s, got %v", EnvSigningJWK, err)
	}
}

func TestLoadSigningKey_Valid(t *testing.T) {
	want, jwk := newKeyJWK(t)
	key, kid, err := LoadSigningKey([]*config.AppConfig{app(t, apiYAML)}, jwk)
	if err != nil || kid != "kid-1" || !key.Equal(want) {
		t.Errorf("unexpected result: kid %q, err %v", kid, err)
	}
}

type recorder struct {
	mu   sync.Mutex
	args map[string]pulumi.MockResourceArgs
}

func (r *recorder) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args[a.Name] = a
	return a.Name + "_id", a.Inputs, nil
}

func (r *recorder) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }

var testNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func provision(t *testing.T, a *config.AppConfig, now time.Time) (*recorder, time.Time, error) {
	t.Helper()
	_, jwk := newKeyJWK(t)
	k, kid, err := LoadSigningKey([]*config.AppConfig{a}, jwk)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{args: map[string]pulumi.MockResourceArgs{}}
	var exp time.Time
	var perr error
	err = pulumi.RunErr(func(ctx *pulumi.Context) error {
		p, err := postgresql.NewProvider(ctx, "pg", &postgresql.ProviderArgs{})
		if err != nil {
			return err
		}
		res, err := apppostgres.ProvisionAppDatabase(ctx, a, "dev", "postgres", p)
		if err != nil {
			return err
		}
		exp, perr = Provision(ctx, a, "dev", "app_blog_dev", k, kid, res,
			secrets.StoreSettings{AccountID: "acct", StoreID: "store"}, now)
		return nil
	}, pulumi.WithMocks("appops", "test", rec))
	if err != nil {
		t.Fatal(err)
	}
	return rec, exp, perr
}

func TestProvision_PublishesTokenForAppRole(t *testing.T) {
	rec, exp, err := provision(t, app(t, apiYAML), testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantExp := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !exp.Equal(wantExp) {
		t.Errorf("expiry = %v, want %v", exp, wantExp)
	}
	a, ok := rec.args["blog-dev-api-token"]
	if !ok {
		t.Fatal("no blog-dev-api-token resource")
	}
	v := a.Inputs["value"]
	if !v.IsSecret() {
		t.Fatal("token is not secret")
	}
	parts := strings.Split(v.SecretValue().Element.StringValue(), ".")
	if len(parts) != 3 {
		t.Fatalf("value is not a JWT")
	}
	raw, _ := b64.DecodeString(parts[1])
	var c struct {
		Role     string
		Iat, Exp int64
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Role != "app_blog_dev" || c.Iat != time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Unix() || c.Exp != wantExp.Unix() {
		t.Errorf("unexpected claims %+v", c)
	}
}

func TestProvision_DependsOnGrants(t *testing.T) {
	rec, _, err := provision(t, app(t, apiYAML), testNow)
	if err != nil {
		t.Fatal(err)
	}
	deps := strings.Join(rec.args["blog-dev-api-token"].RegisterRPC.GetDependencies(), " ")
	for _, want := range []string{"::blog-dev-role", "::blog-dev-schema-usage", "::blog-dev-function-grants", "::blog-dev-api-authenticator"} {
		if !strings.Contains(deps, want) {
			t.Errorf("token secret does not depend on %s (deps: %s)", want, deps)
		}
	}
}

func TestProvision_RefusesExpiredToken(t *testing.T) {
	rec, _, err := provision(t, app(t, apiYAML), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "api.issued_on") {
		t.Fatalf("expected an expiry error mentioning api.issued_on, got %v", err)
	}
	if _, ok := rec.args["blog-dev-api-token"]; ok {
		t.Error("an expired token must not be published")
	}
}

func TestProvision_RejectsAppWithoutAPI(t *testing.T) {
	_, _, err := provision(t, app(t, plainYAML), testNow)
	if err == nil || !strings.Contains(err.Error(), "api.enabled") {
		t.Errorf("expected an error mentioning api.enabled, got %v", err)
	}
}
