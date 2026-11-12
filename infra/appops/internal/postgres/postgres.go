package postgres

import (
	"fmt"
	"strconv"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/config"
	"github.com/pulumi/pulumi-postgresql/sdk/v3/go/postgresql"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// AppResources tracks the provisioned database artifacts for an application
type AppResources struct {
	Schema           *postgresql.Schema
	Role             *postgresql.Role
	Password         *random.RandomPassword
	DatabaseGrant    *postgresql.Grant
	SchemaGrant      *postgresql.Grant
	TableGrant       *postgresql.Grant
	SequenceGrant    *postgresql.Grant
	DefaultPrivTable *postgresql.DefaultPrivileges
	DefaultPrivSeq   *postgresql.DefaultPrivileges
}

// ProvisionAppDatabase creates schema, role, generated password, and appropriate privileges for an opted-in application.
// migrationOwner is the role that executes shared migrations (typically "postgres" on Supabase)
func ProvisionAppDatabase(
	ctx *pulumi.Context,
	app *config.AppConfig,
	env string,
	migrationOwner string,
	pgProvider *postgresql.Provider,
) (*AppResources, error) {
	if migrationOwner == "" {
		migrationOwner = "postgres"
	}

	roleName, err := app.RoleName(env)
	if err != nil {
		return nil, fmt.Errorf("failed to derive role name: %w", err)
	}

	resPrefix := fmt.Sprintf("%s-%s", app.Name, env)

	// 1. PostgreSQL Schema
	// App gets its own isolated schema.
	schema, err := postgresql.NewSchema(ctx, fmt.Sprintf("%s-schema", resPrefix), &postgresql.SchemaArgs{
		Name:     pulumi.String(app.Database.Schema),
		Database: pulumi.String(app.Database.Name),
		Owner:    pulumi.String(migrationOwner),
	}, pulumi.Provider(pgProvider), pulumi.Protect(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create schema resource: %w", err)
	}

	// 2. Stable generated password
	// Uses Pulumi RandomPassword so it remains stable across runs unless explicitly requested.
	// Password is marked as secret automatically.
	// Keepers tie the password to credentials.version: bumping it in appops.yaml
	// regenerates the password. The connection URI builder percent-encodes it.
	version := strconv.Itoa(app.Credentials.Version)
	password, err := random.NewRandomPassword(ctx, fmt.Sprintf("%s-password", resPrefix), &random.RandomPasswordArgs{
		Length:          pulumi.Int(32),
		Special:         pulumi.Bool(true),
		OverrideSpecial: pulumi.String("-_"),
		Keepers:         pulumi.StringMap{"version": pulumi.String(version)},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create password resource: %w", err)
	}

	// 3. PostgreSQL Application Login Role
	// The password goes in the write-only passwordWo field, so it is not read back as a
	// resource output; passwordWoVersion controls when PostgreSQL receives an update.
	// SkipDropRole and Protect guard against unintentional role deletion.
	role, err := postgresql.NewRole(ctx, fmt.Sprintf("%s-role", resPrefix), &postgresql.RoleArgs{
		Name:              pulumi.String(roleName),
		Login:             pulumi.Bool(true),
		PasswordWo:        password.Result,
		PasswordWoVersion: pulumi.String(version),
		SkipDropRole:      pulumi.Bool(true),
	}, pulumi.Provider(pgProvider), pulumi.Protect(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create role resource: %w", err)
	}

	// 4. Database CONNECT privilege
	dbGrant, err := postgresql.NewGrant(ctx, fmt.Sprintf("%s-db-connect", resPrefix), &postgresql.GrantArgs{
		Database:   pulumi.String(app.Database.Name),
		Role:       role.Name,
		ObjectType: pulumi.String("database"),
		Privileges: pulumi.StringArray{
			pulumi.String("CONNECT"),
		},
	}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{role}))
	if err != nil {
		return nil, fmt.Errorf("failed to create database grant resource: %w", err)
	}

	// 5. Schema USAGE privilege
	schemaGrant, err := postgresql.NewGrant(ctx, fmt.Sprintf("%s-schema-usage", resPrefix), &postgresql.GrantArgs{
		Database:   pulumi.String(app.Database.Name),
		Schema:     schema.Name,
		Role:       role.Name,
		ObjectType: pulumi.String("schema"),
		Privileges: pulumi.StringArray{
			pulumi.String("USAGE"),
		},
	}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{schema, role}))
	if err != nil {
		return nil, fmt.Errorf("failed to create schema grant resource: %w", err)
	}

	// 6. Explicit Table Privileges (if tables already exist in schema)
	var tableGrant *postgresql.Grant
	if len(app.Permissions.Tables) > 0 {
		var tablePrivs pulumi.StringArray
		for _, p := range app.Permissions.Tables {
			tablePrivs = append(tablePrivs, pulumi.String(p))
		}

		tableGrant, err = postgresql.NewGrant(ctx, fmt.Sprintf("%s-table-grants", resPrefix), &postgresql.GrantArgs{
			Database:   pulumi.String(app.Database.Name),
			Schema:     schema.Name,
			Role:       role.Name,
			ObjectType: pulumi.String("table"),
			Privileges: tablePrivs,
			Objects:    pulumi.StringArray{}, // empty applies to all tables in schema
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{schema, role}))
		if err != nil {
			return nil, fmt.Errorf("failed to create table grants resource: %w", err)
		}
	}

	// 7. Explicit Sequence Privileges (if sequences already exist in schema)
	var seqGrant *postgresql.Grant
	if len(app.Permissions.Sequences) > 0 {
		var seqPrivs pulumi.StringArray
		for _, p := range app.Permissions.Sequences {
			seqPrivs = append(seqPrivs, pulumi.String(p))
		}

		seqGrant, err = postgresql.NewGrant(ctx, fmt.Sprintf("%s-seq-grants", resPrefix), &postgresql.GrantArgs{
			Database:   pulumi.String(app.Database.Name),
			Schema:     schema.Name,
			Role:       role.Name,
			ObjectType: pulumi.String("sequence"),
			Privileges: seqPrivs,
			Objects:    pulumi.StringArray{}, // empty applies to all sequences in schema
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{schema, role}))
		if err != nil {
			return nil, fmt.Errorf("failed to create sequence grants resource: %w", err)
		}
	}

	// 8. Default Privileges for future tables created by the migration owner
	var defaultPrivTable *postgresql.DefaultPrivileges
	if len(app.Permissions.Tables) > 0 {
		var privs pulumi.StringArray
		for _, p := range app.Permissions.Tables {
			privs = append(privs, pulumi.String(p))
		}

		defaultPrivTable, err = postgresql.NewDefaultPrivileges(ctx, fmt.Sprintf("%s-default-table-privs", resPrefix), &postgresql.DefaultPrivilegesArgs{
			Database:   pulumi.String(app.Database.Name),
			Schema:     schema.Name,
			Owner:      pulumi.String(migrationOwner),
			Role:       role.Name,
			ObjectType: pulumi.String("table"),
			Privileges: privs,
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{schema, role}))
		if err != nil {
			return nil, fmt.Errorf("failed to configure default table privileges: %w", err)
		}
	}

	// 9. Default Privileges for future sequences created by the migration owner
	var defaultPrivSeq *postgresql.DefaultPrivileges
	if len(app.Permissions.Sequences) > 0 {
		var privs pulumi.StringArray
		for _, p := range app.Permissions.Sequences {
			privs = append(privs, pulumi.String(p))
		}

		defaultPrivSeq, err = postgresql.NewDefaultPrivileges(ctx, fmt.Sprintf("%s-default-seq-privs", resPrefix), &postgresql.DefaultPrivilegesArgs{
			Database:   pulumi.String(app.Database.Name),
			Schema:     schema.Name,
			Owner:      pulumi.String(migrationOwner),
			Role:       role.Name,
			ObjectType: pulumi.String("sequence"),
			Privileges: privs,
		}, pulumi.Provider(pgProvider), pulumi.DependsOn([]pulumi.Resource{schema, role}))
		if err != nil {
			return nil, fmt.Errorf("failed to configure default sequence privileges: %w", err)
		}
	}

	return &AppResources{
		Schema:           schema,
		Role:             role,
		Password:         password,
		DatabaseGrant:    dbGrant,
		SchemaGrant:      schemaGrant,
		TableGrant:       tableGrant,
		SequenceGrant:    seqGrant,
		DefaultPrivTable: defaultPrivTable,
		DefaultPrivSeq:   defaultPrivSeq,
	}, nil
}
