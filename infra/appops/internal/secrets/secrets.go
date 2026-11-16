// Package secrets publishes application database credentials to Cloudflare Secrets Store.
package secrets

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/config"
	"github.com/pulumi/pulumi-cloudflare/sdk/v6/go/cloudflare"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// ConnectionSettings describes where applications reach the database.
type ConnectionSettings struct {
	Host string
	Port int
	// UserSuffix is appended to the role name, e.g. ".<project-ref>" for the Supabase pooler.
	UserSuffix string
}

// StoreSettings identifies the Cloudflare Secrets Store to write to.
type StoreSettings struct {
	AccountID string
	StoreID   string
}

// BuildConnectionURI returns a postgresql:// URI with the user and password percent-encoded.
func BuildConnectionURI(conn ConnectionSettings, role, password, database string) string {
	u := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(role+conn.UserSuffix, password),
		Host:   conn.Host + ":" + strconv.Itoa(conn.Port),
		Path:   "/" + database,
	}
	return u.String()
}

// PublishDatabaseURL stores the application's connection URI in Secrets Store.
// The value is derived from the secret password Output, so it stays secret in state.
//
// The Cloudflare value is write-only: Pulumi cannot read it back, so manual edits in
// Cloudflare are not detected as drift. Rotate by bumping credentials.version.
func PublishDatabaseURL(
	ctx *pulumi.Context,
	app *config.AppConfig,
	env string,
	roleName string,
	password pulumi.StringOutput,
	conn ConnectionSettings,
	store StoreSettings,
	deps ...pulumi.Resource,
) (*cloudflare.SecretsStoreSecret, error) {
	uri := password.ApplyT(func(pw string) string {
		return BuildConnectionURI(conn, roleName, pw, app.Database.Name)
	}).(pulumi.StringOutput)

	secret, err := cloudflare.NewSecretsStoreSecret(ctx, fmt.Sprintf("%s-%s-database-url", app.Name, env), &cloudflare.SecretsStoreSecretArgs{
		AccountId: pulumi.String(store.AccountID),
		StoreId:   pulumi.String(store.StoreID),
		Name:      pulumi.String(app.SecretName(env)),
		Value:     uri,
		Scopes:    pulumi.StringArray{pulumi.String("workers")},
		Comment:   pulumi.String("Managed by AppOps"),
	}, pulumi.DependsOn(deps))
	if err != nil {
		return nil, fmt.Errorf("failed to create Secrets Store secret for %s: %w", app.Name, err)
	}
	return secret, nil
}

// PublishAPIToken stores the application's API token in Secrets Store as
// APP_<NAME>_<ENV>_API_TOKEN.
//
// The token is derived from non-secret inputs (time outputs and a key held in a Go
// variable), so its Output is not secret on its own. pulumi-cloudflare v6.23.3's
// NewSecretsStoreSecret already wraps Value with pulumi.ToSecret; the explicit wrap
// here keeps the token secret even if a future SDK version stops doing that.
// Rotate by bumping api.version or credentials.version, not by editing the secret
// in Cloudflare.
func PublishAPIToken(
	ctx *pulumi.Context,
	app *config.AppConfig,
	env string,
	token pulumi.StringOutput,
	store StoreSettings,
	deps ...pulumi.Resource,
) (*cloudflare.SecretsStoreSecret, error) {
	secret, err := cloudflare.NewSecretsStoreSecret(ctx, fmt.Sprintf("%s-%s-api-token", app.Name, env), &cloudflare.SecretsStoreSecretArgs{
		AccountId: pulumi.String(store.AccountID),
		StoreId:   pulumi.String(store.StoreID),
		Name:      pulumi.String(app.APITokenSecretName(env)),
		Value:     pulumi.ToSecret(token).(pulumi.StringOutput),
		Scopes:    pulumi.StringArray{pulumi.String("workers")},
		Comment:   pulumi.String("Managed by AppOps"),
	}, pulumi.DependsOn(deps))
	if err != nil {
		return nil, fmt.Errorf("failed to create API token secret for %s: %w", app.Name, err)
	}
	return secret, nil
}
