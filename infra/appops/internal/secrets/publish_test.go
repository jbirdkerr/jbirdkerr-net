package secrets

import (
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/config"
	"github.com/pulumi/pulumi-postgresql/sdk/v3/go/postgresql"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type recorder struct {
	mu   sync.Mutex
	args map[string]pulumi.MockResourceArgs
	urns map[string]string
}

func (r *recorder) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args[a.Name] = a
	return a.Name + "_id", a.Inputs, nil
}

func (r *recorder) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }

const testToken = "header.payload.signature"

// publish runs PublishAPIToken with a plain (non-secret) token and one dependency.
func publish(t *testing.T) *recorder {
	t.Helper()
	app, err := config.ParseAppConfig([]byte("name: blog\napi:\n  enabled: true\n  issued_on: 2026-11-01\n"), "appops.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{args: map[string]pulumi.MockResourceArgs{}}
	err = pulumi.RunErr(func(ctx *pulumi.Context) error {
		dep, err := postgresql.NewRole(ctx, "dep-role", &postgresql.RoleArgs{Name: pulumi.String("x")})
		if err != nil {
			return err
		}
		token := pulumi.String(testToken).ToStringOutput() // deliberately not secret
		_, err = PublishAPIToken(ctx, app, "dev", token, StoreSettings{AccountID: "acct", StoreID: "store"}, dep)
		return err
	}, pulumi.WithMocks("appops", "test", rec))
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	return rec
}

func TestPublishAPIToken_SecretSettings(t *testing.T) {
	rec := publish(t)
	a, ok := rec.args["blog-dev-api-token"]
	if !ok {
		var names []string
		for n := range rec.args {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Fatalf("no blog-dev-api-token resource; got %v", names)
	}
	in := a.Inputs
	want := map[string]string{
		"name": "APP_BLOG_DEV_API_TOKEN", "accountId": "acct", "storeId": "store", "comment": "Managed by AppOps",
	}
	for k, v := range want {
		if got := in[resource.PropertyKey(k)]; !got.IsString() || got.StringValue() != v {
			t.Errorf("%s = %v, want %q", k, got, v)
		}
	}
	scopes := in["scopes"]
	if !scopes.IsArray() || len(scopes.ArrayValue()) != 1 || scopes.ArrayValue()[0].StringValue() != "workers" {
		t.Errorf("scopes = %v, want [workers]", scopes)
	}
}

// The value sent to Cloudflare must be secret. Both PublishAPIToken and the
// pulumi-cloudflare SDK (v6.23.3) wrap it, so this guards the outcome, not one wrap.
func TestPublishAPIToken_ValueIsSecret(t *testing.T) {
	v := publish(t).args["blog-dev-api-token"].Inputs["value"]
	if !v.IsSecret() {
		t.Fatalf("token value is not secret: %v", v)
	}
	if inner := v.SecretValue().Element; !inner.IsString() || inner.StringValue() != testToken {
		t.Errorf("secret value = %v, want the token", inner)
	}
}

func TestPublishAPIToken_DependsOnGivenResources(t *testing.T) {
	rec := publish(t)
	deps := rec.args["blog-dev-api-token"].RegisterRPC.GetDependencies()
	for _, d := range deps {
		if strings.HasSuffix(d, "::dep-role") {
			return
		}
	}
	t.Errorf("dependencies %v do not include dep-role", deps)
}
