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
