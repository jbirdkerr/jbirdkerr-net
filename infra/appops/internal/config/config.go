package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Standard PostgreSQL identifier limits and constraints
const (
	MaxPostgresIdentifierLength = 63
)

// API token expiry limits, in days.
const (
	DefaultAPIExpiresInDays = 180
	MinAPIExpiresInDays     = 7
	MaxAPIExpiresInDays     = 365
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
	Functions []string `yaml:"functions"`
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

// APIConfig opts an application into a signed REST API token (see docs/OPERATIONS.md).
type APIConfig struct {
	Enabled       bool `yaml:"enabled"`
	ExpiresInDays int  `yaml:"expires_in_days"`
	// IssuedOn (YYYY-MM-DD) anchors the token: iat is midnight UTC that day and exp is
	// IssuedOn + ExpiresInDays. Changing it re-mints the token. Required when Enabled.
	IssuedOn string `yaml:"issued_on"`

	issuedAt time.Time
}

// now is the clock used by validation; tests replace it.
var now = time.Now

// IssuedAt returns midnight UTC on api.issued_on (zero if unset).
func (a APIConfig) IssuedAt() time.Time { return a.issuedAt }

// ExpiresAt returns IssuedAt plus api.expires_in_days.
func (a APIConfig) ExpiresAt() time.Time {
	return a.issuedAt.AddDate(0, 0, a.ExpiresInDays)
}

// AppConfig represents an individual application's appops.yaml
type AppConfig struct {
	Name        string            `yaml:"name"`
	Database    DatabaseConfig    `yaml:"database"`
	Permissions PermissionsConfig `yaml:"permissions"`
	Credentials CredentialsConfig `yaml:"credentials"`
	API         APIConfig         `yaml:"api"`

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

	if c.API.ExpiresInDays == 0 {
		c.API.ExpiresInDays = DefaultAPIExpiresInDays
	}
	if c.API.ExpiresInDays < MinAPIExpiresInDays || c.API.ExpiresInDays > MaxAPIExpiresInDays {
		return fmt.Errorf("field 'api.expires_in_days' must be between %d and %d", MinAPIExpiresInDays, MaxAPIExpiresInDays)
	}

	if issued := strings.TrimSpace(c.API.IssuedOn); issued != "" {
		t, err := time.Parse(time.DateOnly, issued)
		if err != nil {
			return fmt.Errorf("field 'api.issued_on' must be a date in YYYY-MM-DD form, got %q", c.API.IssuedOn)
		}
		// One day of slack covers operators whose local date is ahead of UTC.
		if t.After(now().UTC().AddDate(0, 0, 1)) {
			return fmt.Errorf("field 'api.issued_on' (%s) is in the future", issued)
		}
		c.API.issuedAt = t
	} else if c.API.Enabled {
		return fmt.Errorf("field 'api.issued_on' is required when api.enabled is true (set it to today's date, YYYY-MM-DD)")
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

	// Validate function permissions if provided
	for _, perm := range c.Permissions.Functions {
		if strings.ToUpper(strings.TrimSpace(perm)) != "EXECUTE" {
			return fmt.Errorf("invalid function permission '%s' (only EXECUTE is allowed)", perm)
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
	return c.secretPrefix(env) + "_DATABASE_URL"
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

// APITokenSecretName returns the Cloudflare Secrets Store secret name for the API token: APP_<NAME>_<ENV>_API_TOKEN
func (c *AppConfig) APITokenSecretName(env string) string {
	return c.secretPrefix(env) + "_API_TOKEN"
}

// secretPrefix returns APP_<NAME>_<ENV>, shared by all secret names for an application.
func (c *AppConfig) secretPrefix(env string) string {
	sanitizedApp := strings.ToUpper(SanitizeIdentifier(c.Name))
	sanitizedEnv := strings.ToUpper(SanitizeIdentifier(env))
	return fmt.Sprintf("APP_%s_%s", sanitizedApp, sanitizedEnv)
}
