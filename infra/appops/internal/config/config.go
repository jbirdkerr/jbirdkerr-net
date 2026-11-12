package config

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Standard PostgreSQL identifier limits and constraints
const (
	MaxPostgresIdentifierLength = 63
)

var (
	// PostgreSQL valid identifier regex (alphanumeric + underscore, starts with letter or underscore)
	identifierRegex = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// reservedSchemas are schemas AppOps must never manage: PostgreSQL built-ins and
// Supabase-managed schemas. Matching is case-insensitive; "pg_" prefixes are also reserved.
var reservedSchemas = map[string]bool{
	"public": true, "information_schema": true,
	"auth": true, "storage": true, "extensions": true, "realtime": true, "vault": true,
	"graphql": true, "graphql_public": true, "pgsodium": true, "pgsodium_masks": true,
	"net": true, "cron": true, "pgbouncer": true, "supabase_functions": true,
	"supabase_migrations": true, "_realtime": true, "_analytics": true, "topology": true,
}

func isReservedSchema(name string) bool {
	n := strings.ToLower(name)
	return reservedSchemas[n] || strings.HasPrefix(n, "pg_")
}

// PermissionsConfig specifies database object privileges
type PermissionsConfig struct {
	Tables    []string `yaml:"tables"`
	Sequences []string `yaml:"sequences"`
}

// DatabaseConfig specifies schema and database target settings
type DatabaseConfig struct {
	Name   string `yaml:"name"`
	Schema string `yaml:"schema"`
}

// CredentialsConfig specifies credential handling and rotation settings
type CredentialsConfig struct {
	// Version is the explicit rotation token. Defaults to 1. Increment it to
	// generate a new password and push it to PostgreSQL and Cloudflare.
	Version int `yaml:"version"`
}

// AppConfig represents an individual application's appops.yaml
type AppConfig struct {
	Name        string            `yaml:"name"`
	Database    DatabaseConfig    `yaml:"database"`
	Permissions PermissionsConfig `yaml:"permissions"`
	Credentials CredentialsConfig `yaml:"credentials"`

	// Derived or context metadata
	SourcePath string `yaml:"-"`
}

// ParseAppConfig parses and validates raw YAML into an AppConfig
func ParseAppConfig(data []byte, sourcePath string) (*AppConfig, error) {
	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML in %s: %w", sourcePath, err)
	}
	cfg.SourcePath = sourcePath

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validation error in %s: %w", sourcePath, err)
	}

	return &cfg, nil
}

// SanitizeIdentifier cleans an arbitrary string to be a valid Postgres identifier
func SanitizeIdentifier(s string) string {
	s = strings.ToLower(s)
	// Replace hyphens and other non-alphanumeric chars with underscores
	reg := regexp.MustCompile(`[^a-z0-9_]+`)
	s = reg.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	if len(s) > MaxPostgresIdentifierLength {
		s = s[:MaxPostgresIdentifierLength]
	}
	return s
}

// Validate ensures application configuration is well-formed
func (c *AppConfig) Validate() error {
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return fmt.Errorf("field 'name' cannot be empty")
	}
	if len(name) > MaxPostgresIdentifierLength {
		return fmt.Errorf("field 'name' value '%s' exceeds max PostgreSQL identifier length (%d)", name, MaxPostgresIdentifierLength)
	}

	// Schema default fallback to sanitized name if omitted
	if strings.TrimSpace(c.Database.Schema) == "" {
		c.Database.Schema = SanitizeIdentifier(c.Name)
	}

	// Database name default fallback to "postgres" if omitted
	if strings.TrimSpace(c.Database.Name) == "" {
		c.Database.Name = "postgres"
	}

	if c.Credentials.Version == 0 {
		c.Credentials.Version = 1
	}
	if c.Credentials.Version < 0 {
		return fmt.Errorf("field 'credentials.version' must be a positive integer")
	}

	// Validate Schema identifier
	if err := validateIdentifier(c.Database.Schema, "database.schema"); err != nil {
		return err
	}

	if isReservedSchema(c.Database.Schema) {
		return fmt.Errorf("field 'database.schema' value '%s' is reserved (PostgreSQL or Supabase managed) and cannot be managed by AppOps", c.Database.Schema)
	}

	// Validate Database name identifier
	if err := validateIdentifier(c.Database.Name, "database.name"); err != nil {
		return err
	}

	// Validate default table permissions if provided
	validTablePerms := map[string]bool{
		"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
		"TRUNCATE": true, "REFERENCES": true, "TRIGGER": true,
	}
	for _, perm := range c.Permissions.Tables {
		if !validTablePerms[strings.ToUpper(strings.TrimSpace(perm))] {
			return fmt.Errorf("invalid table permission '%s'", perm)
		}
	}

	// Validate default sequence permissions if provided
	validSeqPerms := map[string]bool{
		"USAGE": true, "SELECT": true, "UPDATE": true,
	}
	for _, perm := range c.Permissions.Sequences {
		if !validSeqPerms[strings.ToUpper(strings.TrimSpace(perm))] {
			return fmt.Errorf("invalid sequence permission '%s'", perm)
		}
	}

	return nil
}

// RoleName returns the deterministic PostgreSQL login role name: app_<sanitized_name>_<env>
func (c *AppConfig) RoleName(env string) (string, error) {
	sanitizedEnv := SanitizeIdentifier(env)
	if sanitizedEnv == "" {
		return "", fmt.Errorf("environment cannot be empty")
	}

	base := fmt.Sprintf("app_%s_%s", SanitizeIdentifier(c.Name), sanitizedEnv)
	if len(base) > MaxPostgresIdentifierLength {
		return "", fmt.Errorf("derived role name '%s' exceeds PostgreSQL maximum identifier length of %d", base, MaxPostgresIdentifierLength)
	}
	return base, nil
}

// SecretName returns the deterministic Cloudflare Secret Store secret name: APP_<NAME>_<ENV>_DATABASE_URL
func (c *AppConfig) SecretName(env string) string {
	sanitizedApp := strings.ToUpper(SanitizeIdentifier(c.Name))
	sanitizedEnv := strings.ToUpper(SanitizeIdentifier(env))
	return fmt.Sprintf("APP_%s_%s_DATABASE_URL", sanitizedApp, sanitizedEnv)
}

func validateIdentifier(name, field string) error {
	name = strings.TrimSpace(name)
	if len(name) > MaxPostgresIdentifierLength {
		return fmt.Errorf("field '%s' value '%s' exceeds max PostgreSQL identifier length (%d)", field, name, MaxPostgresIdentifierLength)
	}
	if !identifierRegex.MatchString(name) {
		return fmt.Errorf("field '%s' value '%s' is not a valid PostgreSQL identifier (must start with letter/underscore and contain only lowercase letters, digits, and underscores)", field, name)
	}
	return nil
}
