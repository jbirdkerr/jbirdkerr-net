package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverApps_OptInFiltering(t *testing.T) {
	tempDir := t.TempDir()

	// 1. App with valid appops.yaml (opted in)
	app1Dir := filepath.Join(tempDir, "app1")
	if err := os.Mkdir(app1Dir, 0755); err != nil {
		t.Fatal(err)
	}
	app1Yaml := `
name: app1
database:
  schema: app1_schema
`
	if err := os.WriteFile(filepath.Join(app1Dir, AppOpsConfigFileName), []byte(app1Yaml), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. App without appops.yaml (static app, not opted in)
	app2Dir := filepath.Join(tempDir, "app2")
	if err := os.Mkdir(app2Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app2Dir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. App with valid appops.yaml (opted in)
	app3Dir := filepath.Join(tempDir, "app3")
	if err := os.Mkdir(app3Dir, 0755); err != nil {
		t.Fatal(err)
	}
	app3Yaml := `
name: app3
`
	if err := os.WriteFile(filepath.Join(app3Dir, AppOpsConfigFileName), []byte(app3Yaml), 0644); err != nil {
		t.Fatal(err)
	}

	apps, err := DiscoverApps(tempDir)
	if err != nil {
		t.Fatalf("unexpected discovery error: %v", err)
	}

	if len(apps) != 2 {
		t.Fatalf("expected 2 discovered apps, got %d", len(apps))
	}

	if apps[0].Name != "app1" || apps[1].Name != "app3" {
		t.Errorf("expected [app1, app3], got [%s, %s]", apps[0].Name, apps[1].Name)
	}
}

func TestDiscoverApps_ConflictHandling(t *testing.T) {
	tempDir := t.TempDir()

	app1Dir := filepath.Join(tempDir, "app1")
	os.Mkdir(app1Dir, 0755)
	app1Yaml := `
name: duplicate-name
database:
  schema: schema_one
`
	os.WriteFile(filepath.Join(app1Dir, AppOpsConfigFileName), []byte(app1Yaml), 0644)

	app2Dir := filepath.Join(tempDir, "app2")
	os.Mkdir(app2Dir, 0755)
	app2Yaml := `
name: duplicate-name
database:
  schema: schema_two
`
	os.WriteFile(filepath.Join(app2Dir, AppOpsConfigFileName), []byte(app2Yaml), 0644)

	_, err := DiscoverApps(tempDir)
	if err == nil {
		t.Fatal("expected conflict error for duplicate app names, got nil")
	}
}

func TestDiscoverApps_SanitizedIdentityCollision(t *testing.T) {
	tempDir := t.TempDir()
	for dir, yaml := range map[string]string{
		"a": "name: my-app\ndatabase:\n  schema: schema_one\n",
		"b": "name: my_app\ndatabase:\n  schema: schema_two\n",
	} {
		os.Mkdir(filepath.Join(tempDir, dir), 0755)
		os.WriteFile(filepath.Join(tempDir, dir, AppOpsConfigFileName), []byte(yaml), 0644)
	}

	if _, err := DiscoverApps(tempDir); err == nil {
		t.Fatal("expected error: 'my-app' and 'my_app' map to the same role identity")
	}
}

func TestDiscoverApps_MultipleAppsIndependent(t *testing.T) {
	tempDir := t.TempDir()
	for dir, yaml := range map[string]string{
		"blog":  "name: blog\n",
		"stats": "name: stats\ndatabase:\n  schema: stats_data\n",
	} {
		os.Mkdir(filepath.Join(tempDir, dir), 0755)
		os.WriteFile(filepath.Join(tempDir, dir, AppOpsConfigFileName), []byte(yaml), 0644)
	}

	apps, err := DiscoverApps(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 2 || apps[0].Database.Schema != "blog" || apps[1].Database.Schema != "stats_data" {
		t.Errorf("unexpected discovery result: %+v", apps)
	}
}
