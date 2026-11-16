// Package apitoken wires API token minting and publication for apps with api.enabled.
package apitoken

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/apijwt"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/config"
	apppostgres "github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/postgres"
	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/secrets"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// EnvSigningJWK names the environment variable that holds the signing key.
const EnvSigningJWK = "SUPABASE_JWT_SIGNING_JWK"

// LoadSigningKey parses the signing JWK when any app has api.enabled. Otherwise it
// returns no key and ignores jwk, so the key is only loaded when something needs it.
// It fails closed: an app that needs a token and a missing or invalid key is an error.
func LoadSigningKey(apps []*config.AppConfig, jwk string) (*ecdsa.PrivateKey, string, error) {
	needed := false
	for _, a := range apps {
		if a.API.Enabled {
			needed = true
			break
		}
	}
	if !needed {
		return nil, "", nil
	}
	if strings.TrimSpace(jwk) == "" {
		return nil, "", fmt.Errorf("an app has api.enabled but %s is not set", EnvSigningJWK)
	}
	key, kid, err := apijwt.ParseSigningJWK(jwk)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", EnvSigningJWK, err)
	}
	return key, kid, nil
}

// ExpiryWarning is how close to expiry a token must be before Provision logs a warning.
const ExpiryWarning = 30 * 24 * time.Hour

// Provision mints the app's token from api.issued_on and api.expires_in_days and
// publishes it after the role and its grants exist. It returns the token's expiry.
// The token is deterministic, so a run with no config change leaves the secret as is.
// It refuses to publish a token that has already expired at now.
func Provision(ctx *pulumi.Context, app *config.AppConfig, env, roleName string,
	key *ecdsa.PrivateKey, kid string, res *apppostgres.AppResources,
	store secrets.StoreSettings, now time.Time) (time.Time, error) {
	if !app.API.Enabled {
		return time.Time{}, fmt.Errorf("%s: api.enabled is false; no token to provision", app.Name)
	}
	if key == nil || res == nil || res.APIAuthenticatorGrant == nil {
		return time.Time{}, errors.New("internal error: signing key or authenticator grant missing")
	}
	iat, exp := app.API.IssuedAt(), app.API.ExpiresAt()
	if !exp.After(now) {
		return time.Time{}, fmt.Errorf("%s: API token expired on %s; set api.issued_on to today's date to re-mint it",
			app.Name, exp.Format(time.DateOnly))
	}
	if exp.Sub(now) < ExpiryWarning {
		ctx.Log.Warn(fmt.Sprintf("%s: API token expires on %s; update api.issued_on to re-mint it",
			app.Name, exp.Format(time.DateOnly)), nil)
	}

	token, err := apijwt.Mint(key, kid, roleName, iat, exp)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: minting API token: %w", app.Name, err)
	}

	deps := []pulumi.Resource{res.Role, res.SchemaGrant, res.APIAuthenticatorGrant}
	if res.FunctionGrant != nil {
		deps = append(deps, res.FunctionGrant)
	}
	tokenOut := pulumi.ToSecret(pulumi.String(token)).(pulumi.StringOutput)
	if _, err := secrets.PublishAPIToken(ctx, app, env, tokenOut, store, deps...); err != nil {
		return time.Time{}, err
	}
	return exp, nil
}
