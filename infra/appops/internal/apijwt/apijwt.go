// Package apijwt parses the AppOps signing key and mints ES256 API tokens for Supabase.
//
// Tokens must be deterministic: Pulumi recomputes them on every run, and a different
// token would change the Cloudflare secret each time. Signing uses RFC 6979 (a nil
// random source in ecdsa.PrivateKey.Sign), so identical inputs give an identical token.
//
// Errors never include key material.
package apijwt

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Issuer is the iss claim on every token AppOps mints.
const Issuer = "appops"

var b64 = base64.RawURLEncoding

type signingJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d"`
}

// ParseSigningJWK parses a P-256 private JWK and returns the key and its kid.
// It rejects the JWK if its x/y do not match the public key derived from d.
func ParseSigningJWK(jwk string) (*ecdsa.PrivateKey, string, error) {
	var k signingJWK
	if err := json.Unmarshal([]byte(jwk), &k); err != nil {
		return nil, "", errors.New("signing JWK is not valid JSON")
	}
	if k.Kty != "EC" {
		return nil, "", fmt.Errorf("signing JWK kty must be EC, got %q", k.Kty)
	}
	if k.Crv != "P-256" {
		return nil, "", fmt.Errorf("signing JWK crv must be P-256, got %q", k.Crv)
	}
	if k.Kid == "" {
		return nil, "", errors.New("signing JWK has no kid")
	}
	d, err := b64.DecodeString(k.D)
	if err != nil || len(d) == 0 {
		return nil, "", errors.New("signing JWK has no valid private key (d)")
	}
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		return nil, "", errors.New("signing JWK has no valid private key (d)")
	}
	x, errX := b64.DecodeString(k.X)
	y, errY := b64.DecodeString(k.Y)
	pub, errPub := key.PublicKey.Bytes() // 0x04 || X || Y
	if errX != nil || errY != nil || errPub != nil ||
		!bytes.Equal(append(append([]byte{4}, x...), y...), pub) {
		return nil, "", errors.New("signing JWK x/y do not match the public key derived from d")
	}
	return key, k.Kid, nil
}

// Field order is fixed by these structs, so the encoded token is stable.
type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type claims struct {
	Role string `json:"role"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
	Iss  string `json:"iss"`
}

// Mint returns a deterministic ES256 JWT for role, signed with key.
func Mint(key *ecdsa.PrivateKey, kid, role string, iat, exp time.Time) (string, error) {
	if key == nil {
		return "", errors.New("signing key is nil")
	}
	if kid == "" {
		return "", errors.New("kid is empty")
	}
	if role == "" {
		return "", errors.New("role is empty")
	}
	if !exp.After(iat) {
		return "", errors.New("exp must be after iat")
	}

	h, err := json.Marshal(header{Alg: "ES256", Kid: kid, Typ: "JWT"})
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims{Role: role, Iat: iat.Unix(), Exp: exp.Unix(), Iss: Issuer})
	if err != nil {
		return "", err
	}
	input := b64.EncodeToString(h) + "." + b64.EncodeToString(c)

	digest := sha256.Sum256([]byte(input))
	der, err := key.Sign(nil, digest[:], crypto.SHA256) // nil random source: RFC 6979
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}
	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		return "", fmt.Errorf("decoding signature: %w", err)
	}
	raw := make([]byte, 64) // JWS ES256: r || s, each left-padded to 32 bytes
	sig.R.FillBytes(raw[:32])
	sig.S.FillBytes(raw[32:])

	return input + "." + b64.EncodeToString(raw), nil
}
