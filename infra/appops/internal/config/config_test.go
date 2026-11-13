package config

import (
	"strings"
	"testing"
)

func TestParseAppConfig_Valid(t *testing.T) {
	yamlData := `
name: metrics-worker
database:
  name: postgres
  schema: metrics_worker
permissions:
  tables:
    - SELECT
    - INSERT
    - UPDATE
    - DELETE
  sequences:
    - USAGE
    - SELECT
credentials:
  version: 1
`
	cfg, err := ParseAppConfig([]byte(yamlData), "apps/metrics-worker/appops.yaml")
	if err != nil {
		t.Fatalf("unexpected error parsing valid config: %v", err)
	}

	if cfg.Name != "metrics-worker" {
		t.Errorf("expected name 'metrics-worker', got '%s'", cfg.Name)
	}
	if cfg.Database.Schema != "metrics_worker" {
		t.Errorf("expected schema 'metrics_worker', got '%s'", cfg.Database.Schema)
	}
	if cfg.Database.Name != "postgres" {
		t.Errorf("expected database 'postgres', got '%s'", cfg.Database.Name)
	}
	if len(cfg.Permissions.Tables) != 4 {
		t.Errorf("expected 4 table permissions, got %d", len(cfg.Permissions.Tables))
	}
	if len(cfg.Permissions.Sequences) != 2 {
		t.Errorf("expected 2 sequence permissions, got %d", len(cfg.Permissions.Sequences))
	}
	if cfg.Credentials.Version != 1 {
		t.Errorf("expected credentials version 1")
	}

	roleName, err := cfg.RoleName("dev")
	if err != nil {
		t.Fatalf("unexpected error generating role name: %v", err)
	}
	if roleName != "app_metrics_worker_dev" {
		t.Errorf("expected roleName 'app_metrics_worker_dev', got '%s'", roleName)
	}

	secretName := cfg.SecretName("dev")
	if secretName != "APP_METRICS_WORKER_DEV_DATABASE_URL" {
		t.Errorf("expected secretName 'APP_METRICS_WORKER_DEV_DATABASE_URL', got '%s'", secretName)
	}
}

func TestParseAppConfig_Defaults(t *testing.T) {
	yamlData := `
name: my-new-service
`
	cfg, err := ParseAppConfig([]byte(yamlData), "apps/my-new-service/appops.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Schema defaults to sanitized application name
	if cfg.Database.Schema != "my_new_service" {
		t.Errorf("expected default schema 'my_new_service', got '%s'", cfg.Database.Schema)
	}
	// Database defaults to postgres
	if cfg.Database.Name != "postgres" {
		t.Errorf("expected default database 'postgres', got '%s'", cfg.Database.Name)
	}
}

func TestParseAppConfig_InvalidIdentifiers(t *testing.T) {
	tests := []struct {
		name      string
		yamlData  string
		errSubstr string
	}{
		{
			name: "missing name",
			yamlData: `
database:
  schema: valid_schema
`,
			errSubstr: "field 'name' cannot be empty",
		},
		{
			name: "invalid schema with special chars",
			yamlData: `
name: testapp
database:
  schema: invalid-schema-with-hyphens
`,
			errSubstr: "is not a valid PostgreSQL identifier",
		},
		{
			name: "invalid table permission",
			yamlData: `
name: testapp
permissions:
  tables:
    - SUPERUSER_PERM
`,
			errSubstr: "invalid table permission",
		},
		{
			name: "identifier too long",
			yamlData: `
name: ` + strings.Repeat("a", 65) + `
`,
			errSubstr: "exceeds max PostgreSQL identifier length",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAppConfig([]byte(tc.yamlData), "test.yaml")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("expected error containing %q, got %v", tc.errSubstr, err)
			}
		})
	}
}

func TestParseAppConfig_CredentialsVersion(t *testing.T) {
	cfg, err := ParseAppConfig([]byte("name: app\n"), "appops.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Credentials.Version != 1 {
		t.Errorf("expected default version 1, got %d", cfg.Credentials.Version)
	}

	cfg, err = ParseAppConfig([]byte("name: app\ncredentials:\n  version: 3\n"), "appops.yaml")
	if err != nil || cfg.Credentials.Version != 3 {
		t.Errorf("expected version 3, got %v (err %v)", cfg, err)
	}

	if _, err := ParseAppConfig([]byte("name: app\ncredentials:\n  version: -1\n"), "appops.yaml"); err == nil {
		t.Error("expected error for negative version")
	}
}

func TestParseAppConfig_ReservedSchemas(t *testing.T) {
	for _, schema := range []string{"public", "PUBLIC", "auth", "storage", "pg_catalog", "information_schema"} {
		yamlData := "name: app\ndatabase:\n  schema: " + schema + "\n"
		if _, err := ParseAppConfig([]byte(yamlData), "appops.yaml"); err == nil {
			t.Errorf("expected error for reserved schema %q", schema)
		}
	}

	// A sanitized default that lands on a reserved name must also be rejected.
	if _, err := ParseAppConfig([]byte("name: public\n"), "appops.yaml"); err == nil {
		t.Error("expected error when default schema derived from name is reserved")
	}
}

func TestParseAppConfig_APIDefaultsWhenOmitted(t *testing.T) {
	cfg, err := ParseAppConfig([]byte("name: app\n"), "appops.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.Enabled {
		t.Error("expected api.enabled to default to false")
	}
	if cfg.API.ExpiresInDays != 180 {
		t.Errorf("expected default api.expires_in_days 180, got %d", cfg.API.ExpiresInDays)
	}
	if cfg.API.Version != 1 {
		t.Errorf("expected default api.version 1, got %d", cfg.API.Version)
	}
}

func TestParseAppConfig_APIExplicitValues(t *testing.T) {
	yamlData := "name: app\napi:\n  enabled: true\n  expires_in_days: 30\n  version: 4\n"
	cfg, err := ParseAppConfig([]byte(yamlData), "appops.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.API.Enabled || cfg.API.ExpiresInDays != 30 || cfg.API.Version != 4 {
		t.Errorf("expected enabled/30/4, got %+v", cfg.API)
	}
}

func TestParseAppConfig_APIExpiryBounds(t *testing.T) {
	for _, days := range []string{"7", "365"} {
		if _, err := ParseAppConfig([]byte("name: app\napi:\n  expires_in_days: "+days+"\n"), "appops.yaml"); err != nil {
			t.Errorf("expected expires_in_days %s to be accepted, got %v", days, err)
		}
	}
	for _, days := range []string{"6", "366", "-1"} {
		_, err := ParseAppConfig([]byte("name: app\napi:\n  expires_in_days: "+days+"\n"), "appops.yaml")
		if err == nil || !strings.Contains(err.Error(), "api.expires_in_days") {
			t.Errorf("expected api.expires_in_days error for %s, got %v", days, err)
		}
	}
}

func TestParseAppConfig_APIVersionNegative(t *testing.T) {
	_, err := ParseAppConfig([]byte("name: app\napi:\n  version: -1\n"), "appops.yaml")
	if err == nil || !strings.Contains(err.Error(), "api.version") {
		t.Errorf("expected api.version error, got %v", err)
	}
}

func TestParseAppConfig_FunctionPermissions(t *testing.T) {
	cfg, err := ParseAppConfig([]byte("name: app\npermissions:\n  functions:\n    - execute\n"), "appops.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Permissions.Functions) != 1 {
		t.Errorf("expected 1 function permission, got %d", len(cfg.Permissions.Functions))
	}

	_, err = ParseAppConfig([]byte("name: app\npermissions:\n  functions:\n    - ALL\n"), "appops.yaml")
	if err == nil || !strings.Contains(err.Error(), "invalid function permission") {
		t.Errorf("expected invalid function permission error, got %v", err)
	}
}

func TestAPITokenSecretName(t *testing.T) {
	cfg := &AppConfig{Name: "metrics-worker"}
	if got := cfg.APITokenSecretName("prod"); got != "APP_METRICS_WORKER_PROD_API_TOKEN" {
		t.Errorf("got %q, want APP_METRICS_WORKER_PROD_API_TOKEN", got)
	}
}
