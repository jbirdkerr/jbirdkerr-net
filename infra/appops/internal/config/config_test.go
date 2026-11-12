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
