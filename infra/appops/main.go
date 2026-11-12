package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/discovery"
	apppostgres "github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/postgres"
	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/secrets"
	"github.com/pulumi/pulumi-postgresql/sdk/v3/go/postgresql"
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

		migrationOwner := cfg.Get("migrationOwner")
		if migrationOwner == "" {
			migrationOwner = "postgres"
		}

		// Locate apps directory relative to repository root
		// infra/appops -> ../../apps
		appsDir := filepath.Clean(filepath.Join(".", "..", "..", "apps"))

		apps, err := discovery.DiscoverApps(appsDir)
		if err != nil {
			return fmt.Errorf("app discovery failed: %w", err)
		}

		ctx.Log.Info(fmt.Sprintf("Discovered %d opted-in application(s) in %s for environment '%s'", len(apps), appsDir, env), nil)

		// Instantiate PostgreSQL Provider for the target application database
		// Reads TARGET_PG* if set, otherwise standard PG* from environment
		pgHost := cfg.Get("targetPgHost")
		if pgHost == "" {
			pgHost = os.Getenv("TARGET_PGHOST")
			if pgHost == "" {
				pgHost = os.Getenv("PGHOST")
			}
		}

		pgPort := 5432
		if p := os.Getenv("TARGET_PGPORT"); p != "" {
			if parsed, err := strconv.Atoi(p); err == nil {
				pgPort = parsed
			}
		} else if p := os.Getenv("PGPORT"); p != "" {
			if parsed, err := strconv.Atoi(p); err == nil {
				pgPort = parsed
			}
		}

		pgUser := os.Getenv("TARGET_PGUSER")
		if pgUser == "" {
			pgUser = os.Getenv("PGUSER")
		}

		pgPassword := os.Getenv("TARGET_PGPASSWORD")
		if pgPassword == "" {
			pgPassword = os.Getenv("PGPASSWORD")
		}

		pgDatabase := os.Getenv("TARGET_PGDATABASE")
		if pgDatabase == "" {
			pgDatabase = os.Getenv("PGDATABASE")
			if pgDatabase == "" {
				pgDatabase = "postgres"
			}
		}

		pgSslMode := os.Getenv("TARGET_PGSSLMODE")
		if pgSslMode == "" {
			pgSslMode = os.Getenv("PGSSLMODE")
			if pgSslMode == "" {
				pgSslMode = "require"
			}
		}

		pgProviderArgs := &postgresql.ProviderArgs{}
		if pgHost != "" {
			pgProviderArgs.Host = pulumi.String(pgHost)
			pgProviderArgs.Port = pulumi.Int(pgPort)
			pgProviderArgs.Username = pulumi.String(pgUser)
			pgProviderArgs.Password = pulumi.ToSecret(pulumi.String(pgPassword)).(pulumi.StringInput)
			pgProviderArgs.Database = pulumi.String(pgDatabase)
			pgProviderArgs.Sslmode = pulumi.String(pgSslMode)
		}

		pgProvider, err := postgresql.NewProvider(ctx, "target-pg-provider", pgProviderArgs)
		if err != nil {
			return fmt.Errorf("failed to initialize target PostgreSQL provider: %w", err)
		}

		// Cloudflare Secrets Store target (store is created once, manually; see RECONNAISSANCE.md 3a)
		accountID := cfg.Get("cloudflareAccountId")
		if accountID == "" {
			accountID = os.Getenv("CLOUDFLARE_ACCOUNT_ID")
		}
		if accountID == "" {
			return fmt.Errorf("Cloudflare account ID missing: set appops:cloudflareAccountId or CLOUDFLARE_ACCOUNT_ID")
		}
		storeID := cfg.Get("secretsStoreId")
		if storeID == "" {
			storeID = os.Getenv("CLOUDFLARE_SECRETS_STORE_ID")
		}
		if storeID == "" {
			return fmt.Errorf("Secrets Store ID missing: set appops:secretsStoreId or CLOUDFLARE_SECRETS_STORE_ID")
		}
		store := secrets.StoreSettings{AccountID: accountID, StoreID: storeID}

		// Where applications connect (may differ from the admin connection above)
		connHost := cfg.Get("appDbHost")
		if connHost == "" {
			connHost = pgHost
		}
		if connHost == "" {
			return fmt.Errorf("application database host missing: set appops:appDbHost or TARGET_PGHOST")
		}
		connPort := pgPort
		if p, err := cfg.TryInt("appDbPort"); err == nil {
			connPort = p
		}
		conn := secrets.ConnectionSettings{Host: connHost, Port: connPort, UserSuffix: cfg.Get("appDbUserSuffix")}

		var discoveredAppNames []string
		for _, app := range apps {
			roleName, err := app.RoleName(env)
			if err != nil {
				return err
			}
			secretName := app.SecretName(env)

			ctx.Log.Info(fmt.Sprintf("-> Provisioning App: %s [schema=%s, role=%s, secret=%s]", app.Name, app.Database.Schema, roleName, secretName), nil)
			discoveredAppNames = append(discoveredAppNames, app.Name)

			// Provision PostgreSQL resources: schema, role, random password, grants, default privileges
			res, err := apppostgres.ProvisionAppDatabase(ctx, app, env, migrationOwner, pgProvider)
			if err != nil {
				return fmt.Errorf("failed provisioning PostgreSQL resources for %s: %w", app.Name, err)
			}

			// Publish the connection URI only after the role and its grants exist
			deps := []pulumi.Resource{res.Role, res.DatabaseGrant, res.SchemaGrant}
			if _, err := secrets.PublishDatabaseURL(ctx, app, env, roleName, res.Password.Result, conn, store, deps...); err != nil {
				return err
			}
		}

		ctx.Export("environment", pulumi.String(env))
		ctx.Export("discoveredAppsCount", pulumi.Int(len(apps)))
		ctx.Export("discoveredApps", pulumi.ToStringArray(discoveredAppNames))

		return nil
	})
}
