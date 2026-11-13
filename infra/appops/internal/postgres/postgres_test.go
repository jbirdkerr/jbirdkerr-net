package postgres

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
	mu     sync.Mutex
	names  []string
	inputs map[string]resource.PropertyMap
}

func (r *recorder) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, args.Name)
	r.inputs[args.Name] = args.Inputs
	return args.Name + "_id", args.Inputs, nil
}

func (r *recorder) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func newApp(t *testing.T, yaml string) *config.AppConfig {
	t.Helper()
	app, err := config.ParseAppConfig([]byte(yaml), "appops.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// provision runs ProvisionAppDatabase for the given apps and returns the sorted
// resource names and the recorded inputs.
func provision(t *testing.T, apps ...*config.AppConfig) ([]string, map[string]resource.PropertyMap) {
	t.Helper()
	rec := &recorder{inputs: map[string]resource.PropertyMap{}}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		provider, err := postgresql.NewProvider(ctx, "pg", &postgresql.ProviderArgs{})
		if err != nil {
			return err
		}
		for _, app := range apps {
			if _, err := ProvisionAppDatabase(ctx, app, "dev", "postgres", provider); err != nil {
				return err
			}
		}
		return nil
	}, pulumi.WithMocks("appops", "test", rec))
	if err != nil {
		t.Fatalf("provisioning failed: %v", err)
	}
	var names []string
	for _, n := range rec.names {
		if n != "pg" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, rec.inputs
}

const (
	blogYAML  = "name: blog\npermissions:\n  tables: [SELECT, INSERT]\n  sequences: [USAGE]\n"
	statsYAML = "name: stats\npermissions:\n  tables: [SELECT]\n"
)

func TestProvision_PerAppResourcesAreNamespaced(t *testing.T) {
	names, _ := provision(t, newApp(t, blogYAML), newApp(t, statsYAML))
	for _, n := range names {
		if !strings.HasPrefix(n, "blog-dev-") && !strings.HasPrefix(n, "stats-dev-") {
			t.Errorf("resource %q is not namespaced to an app", n)
		}
	}
}

func TestProvision_AddingAppLeavesExistingResourcesUnchanged(t *testing.T) {
	before, beforeInputs := provision(t, newApp(t, blogYAML))
	after, afterInputs := provision(t, newApp(t, blogYAML), newApp(t, statsYAML))

	set := map[string]bool{}
	for _, n := range after {
		set[n] = true
	}
	for _, n := range before {
		if !set[n] {
			t.Errorf("blog resource %q disappeared after adding stats", n)
		}
		if !beforeInputs[n].DeepEquals(afterInputs[n]) {
			t.Errorf("blog resource %q inputs changed after adding stats", n)
		}
	}
	for _, n := range after {
		isBlog := strings.HasPrefix(n, "blog-dev-")
		found := false
		for _, b := range before {
			if b == n {
				found = true
			}
		}
		if isBlog != found {
			t.Errorf("unexpected new/removed resource %q", n)
		}
	}
}

func TestProvision_PermissionChangeOnlyAffectsThatApp(t *testing.T) {
	_, base := provision(t, newApp(t, blogYAML), newApp(t, statsYAML))
	widerStats := "name: stats\npermissions:\n  tables: [SELECT, UPDATE]\n"
	_, changed := provision(t, newApp(t, blogYAML), newApp(t, widerStats))

	for name, in := range base {
		if strings.HasPrefix(name, "blog-dev-") && !in.DeepEquals(changed[name]) {
			t.Errorf("blog resource %q changed when only stats permissions changed", name)
		}
	}
	if base["stats-dev-table-grants"].DeepEquals(changed["stats-dev-table-grants"]) {
		t.Error("expected stats table grants to change")
	}
}

func TestProvision_NoPublicSchemaGrants(t *testing.T) {
	_, inputs := provision(t, newApp(t, blogYAML))
	for name, in := range inputs {
		if v, ok := in["schema"]; ok && v.IsString() && v.StringValue() == "public" {
			t.Errorf("resource %q targets the public schema", name)
		}
	}
}

// Resource names are part of Pulumi state; renaming one replaces a live resource.
func TestProvision_BaselineResourceNamesUnchanged(t *testing.T) {
	names, _ := provision(t, newApp(t, blogYAML))
	want := []string{
		"blog-dev-db-connect", "blog-dev-default-seq-privs", "blog-dev-default-table-privs",
		"blog-dev-password", "blog-dev-role", "blog-dev-schema", "blog-dev-schema-usage",
		"blog-dev-seq-grants", "blog-dev-table-grants",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("resource names changed:\n got  %v\n want %v", names, want)
	}
}

func str(t *testing.T, in resource.PropertyMap, key string) string {
	t.Helper()
	v, ok := in[resource.PropertyKey(key)]
	if !ok || !v.IsString() {
		t.Fatalf("input %q missing or not a string: %v", key, in)
	}
	return v.StringValue()
}

func strs(t *testing.T, in resource.PropertyMap, key string) []string {
	t.Helper()
	v, ok := in[resource.PropertyKey(key)]
	if !ok || !v.IsArray() {
		t.Fatalf("input %q missing or not an array: %v", key, in)
	}
	var out []string
	for _, e := range v.ArrayValue() {
		out = append(out, e.StringValue())
	}
	return out
}

func TestProvision_APIEnabledGrantsRoleToAuthenticator(t *testing.T) {
	names, inputs := provision(t, newApp(t, blogYAML+"api:\n  enabled: true\n  issued_on: 2026-11-01\n"))
	in, ok := inputs["blog-dev-api-authenticator"]
	if !ok {
		t.Fatalf("no authenticator grant; resources: %v", names)
	}
	if got := str(t, in, "role"); got != "authenticator" {
		t.Errorf("membership recipient = %q, want authenticator", got)
	}
	if got := str(t, in, "grantRole"); got != "app_blog_dev" {
		t.Errorf("granted role = %q, want app_blog_dev", got)
	}
	if v, ok := in["withAdminOption"]; ok && v.IsBool() && v.BoolValue() {
		t.Error("authenticator must not get ADMIN on the app role")
	}
}

func TestProvision_FunctionPermissionsGrantExecute(t *testing.T) {
	names, inputs := provision(t, newApp(t, blogYAML+"  functions: [execute]\n"))
	grant, ok := inputs["blog-dev-function-grants"]
	if !ok {
		t.Fatalf("no function grant; resources: %v", names)
	}
	if str(t, grant, "objectType") != "function" || str(t, grant, "schema") != "blog" ||
		str(t, grant, "role") != "app_blog_dev" || strings.Join(strs(t, grant, "privileges"), ",") != "EXECUTE" {
		t.Errorf("unexpected function grant inputs: %v", grant)
	}
	if objs := strs(t, grant, "objects"); len(objs) != 0 {
		t.Errorf("function grant should cover all functions (empty objects), got %v", objs)
	}
	def, ok := inputs["blog-dev-default-function-privs"]
	if !ok {
		t.Fatalf("no default function privileges; resources: %v", names)
	}
	if str(t, def, "objectType") != "function" || str(t, def, "owner") != "postgres" ||
		str(t, def, "role") != "app_blog_dev" || strings.Join(strs(t, def, "privileges"), ",") != "EXECUTE" {
		t.Errorf("unexpected default function privilege inputs: %v", def)
	}
}

func TestProvision_NewOptionsOffAddNothing(t *testing.T) {
	names, _ := provision(t, newApp(t, blogYAML+"api:\n  enabled: false\n"))
	for _, n := range names {
		if strings.Contains(n, "authenticator") || strings.Contains(n, "function") {
			t.Errorf("unexpected resource %q when api and functions are off", n)
		}
	}
}

func TestProvision_ReturnsNewResources(t *testing.T) {
	check := func(yaml string, wantAPI, wantFn bool) {
		t.Helper()
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			p, err := postgresql.NewProvider(ctx, "pg", &postgresql.ProviderArgs{})
			if err != nil {
				return err
			}
			res, err := ProvisionAppDatabase(ctx, newApp(t, yaml), "dev", "postgres", p)
			if err != nil {
				return err
			}
			if (res.APIAuthenticatorGrant != nil) != wantAPI {
				t.Errorf("APIAuthenticatorGrant set = %v, want %v", res.APIAuthenticatorGrant != nil, wantAPI)
			}
			if (res.FunctionGrant != nil) != wantFn || (res.DefaultPrivFunction != nil) != wantFn {
				t.Errorf("function resources set = %v/%v, want %v", res.FunctionGrant != nil, res.DefaultPrivFunction != nil, wantFn)
			}
			return nil
		}, pulumi.WithMocks("appops", "test", &recorder{inputs: map[string]resource.PropertyMap{}}))
		if err != nil {
			t.Fatal(err)
		}
	}
	check(blogYAML, false, false)
	check(blogYAML+"  functions: [EXECUTE]\napi:\n  enabled: true\n  issued_on: 2026-11-01\n", true, true)
}
