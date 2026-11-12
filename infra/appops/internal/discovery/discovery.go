package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/config"
)

const (
	AppOpsConfigFileName = "appops.yaml"
)

// DiscoverApps searches the immediate children of appsDir for appops.yaml configurations.
// If an application directory does not contain appops.yaml, it is ignored (opt-in design).
func DiscoverApps(appsDir string) ([]*config.AppConfig, error) {
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read applications directory %s: %w", appsDir, err)
	}

	var configs []*config.AppConfig
	seenNames := make(map[string]string)
	seenSchemas := make(map[string]string)
	seenIdentities := make(map[string]string)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		appDir := filepath.Join(appsDir, entry.Name())
		configFile := filepath.Join(appDir, AppOpsConfigFileName)

		// Check if appops.yaml exists
		info, err := os.Stat(configFile)
		if err != nil {
			if os.IsNotExist(err) {
				// App did not opt in, skip
				continue
			}
			return nil, fmt.Errorf("error accessing config file %s: %w", configFile, err)
		}

		if info.IsDir() {
			continue
		}

		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %s: %w", configFile, err)
		}

		cfg, err := config.ParseAppConfig(data, configFile)
		if err != nil {
			return nil, err
		}

		// Prevent duplicate application names
		if existing, exists := seenNames[cfg.Name]; exists {
			return nil, fmt.Errorf("conflicting application name '%s' found in %s and %s", cfg.Name, existing, configFile)
		}
		seenNames[cfg.Name] = configFile

		// Different names can sanitize to the same role and secret identity
		// (e.g. "my-app" and "my_app"), which would collide in PostgreSQL and Cloudflare.
		identity := config.SanitizeIdentifier(cfg.Name)
		if existing, exists := seenIdentities[identity]; exists {
			return nil, fmt.Errorf("application names in %s and %s both map to role/secret identity '%s'", existing, configFile, identity)
		}
		seenIdentities[identity] = configFile

		// Prevent duplicate schema assignments across applications
		if existing, exists := seenSchemas[cfg.Database.Schema]; exists {
			return nil, fmt.Errorf("conflicting schema '%s' assigned to both %s and %s", cfg.Database.Schema, existing, configFile)
		}
		seenSchemas[cfg.Database.Schema] = configFile

		configs = append(configs, cfg)
	}

	// Sort deterministically by name
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})

	return configs, nil
}
