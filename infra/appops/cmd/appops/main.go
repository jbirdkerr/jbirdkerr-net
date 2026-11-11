package main

import (
	"fmt"
	"path/filepath"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/discovery"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "appops")
		env := cfg.Get("environment")
		if env == "" {
			env = "dev"
		}

		// Locate apps directory relative to repository root
		// infra/appops -> ../../apps
		appsDir := filepath.Clean(filepath.Join(".", "..", "..", "apps"))

		apps, err := discovery.DiscoverApps(appsDir)
		if err != nil {
			return fmt.Errorf("app discovery failed: %w", err)
		}

		ctx.Log.Info(fmt.Sprintf("Discovered %d opted-in application(s) in %s for environment '%s'", len(apps), appsDir, env), nil)

		var discoveredAppNames []string
		for _, app := range apps {
			roleName, err := app.RoleName(env)
			if err != nil {
				return err
			}
			secretName := app.SecretName(env)

			ctx.Log.Info(fmt.Sprintf("-> App: %s [schema=%s, role=%s, secret=%s]", app.Name, app.Database.Schema, roleName, secretName), nil)
			discoveredAppNames = append(discoveredAppNames, app.Name)

			// In Milestone 2 & 3: Provision PostgreSQL resources and Cloudflare Secret resources here.
		}

		ctx.Export("environment", pulumi.String(env))
		ctx.Export("discoveredAppsCount", pulumi.Int(len(apps)))
		ctx.Export("discoveredApps", pulumi.ToStringArray(discoveredAppNames))

		return nil
	})
}
