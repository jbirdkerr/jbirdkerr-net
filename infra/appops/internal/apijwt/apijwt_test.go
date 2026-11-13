package apijwt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

// testJWK builds a private JWK for key; mutate lets a test corrupt a field.
func testJWK(t *testing.T, key *ecdsa.PrivateKey, kid string, mutate func(map[string]string)) string {
	t.Helper()
	pub, err := key.PublicKey.Bytes() // 0x04 || X || Y
	if err != nil {
		t.Fatal(err)
	}
	d, err := key.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{
		"kty": "EC", "crv": "P-256", "kid": kid, "alg": "ES256",
		"x": b64.EncodeToString(pub[1:33]), "y": b64.EncodeToString(pub[33:65]), "d": b64.EncodeToString(d),
	}
	if mutate != nil {
		mutate(m)
	}
	out, _ := json.Marshal(m)
	return string(out)
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var (
	testIat = time.Unix(1_800_000_000, 0)
	testExp = testIat.Add(180 * 24 * time.Hour)
)

func TestParseSigningJWK_Valid(t *testing.T) {
	key := newKey(t)
	got, kid, err := ParseSigningJWK(testJWK(t, key, "kid-1", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kid != "kid-1" {
		t.Errorf("kid = %q, want kid-1", kid)
	}
	if !got.Equal(key) {
		t.Error("parsed key does not equal the original")
	}
}

func TestParseSigningJWK_Rejects(t *testing.T) {
	key := newKey(t)
	other := newKey(t)
	otherPub, _ := other.PublicKey.Bytes()
	cases := map[string]struct{ jwk, want string }{
		"malformed json": {"{not json", "not valid JSON"},
		"wrong kty":      {testJWK(t, key, "k", func(m map[string]string) { m["kty"] = "RSA" }), "kty"},
		"wrong curve":    {testJWK(t, key, "k", func(m map[string]string) { m["crv"] = "P-384" }), "crv"},
		"missing kid":    {testJWK(t, key, "", nil), "kid"},
		"missing d":      {testJWK(t, key, "k", func(m map[string]string) { delete(m, "d") }), "private key"},
		"bad d encoding": {testJWK(t, key, "k", func(m map[string]string) { m["d"] = "!!!" }), "private key"},
		"x/y do not match d": {testJWK(t, key, "k", func(m map[string]string) {
			m["x"] = b64.EncodeToString(otherPub[1:33])
			m["y"] = b64.EncodeToString(otherPub[33:65])
		}), "public key"},
	}
	d, _ := key.Bytes()
	secret := b64.EncodeToString(d)
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := ParseSigningJWK(tc.jwk)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error mentioning %q, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Error("error message contains the private key")
			}
		})
	}
}

func decodePart(t *testing.T, part string, v any) {
	t.Helper()
	raw, err := b64.DecodeString(part)
	if err != nil {
		t.Fatalf("part is not base64url: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("part is not JSON: %v", err)
	}
}

func TestMint_HeaderAndClaims(t *testing.T) {
	tok, err := Mint(newKey(t), "kid-1", "app_x_prod", testIat, testExp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	var h map[string]any
	decodePart(t, parts[0], &h)
	if h["alg"] != "ES256" || h["kid"] != "kid-1" || h["typ"] != "JWT" || len(h) != 3 {
		t.Errorf("unexpected header %v", h)
	}
	var c map[string]any
	decodePart(t, parts[1], &c)
	if c["role"] != "app_x_prod" || c["iss"] != "appops" ||
		c["iat"] != float64(testIat.Unix()) || c["exp"] != float64(testExp.Unix()) || len(c) != 4 {
		t.Errorf("unexpected claims %v", c)
	}
}

func TestMint_SignatureVerifies(t *testing.T) {
	key := newKey(t)
	tok, err := Mint(key, "kid-1", "app_x_prod", testIat, testExp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := strings.Split(tok, ".")
	sig, err := b64.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature must be 64 raw bytes, got %d (err %v)", len(sig), err)
	}
	der, _ := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])})
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.VerifyASN1(&key.PublicKey, digest[:], der) {
		t.Error("signature does not verify with the public key")
	}
}

func TestMint_Deterministic(t *testing.T) {
	key := newKey(t)
	a, err1 := Mint(key, "kid-1", "app_x_prod", testIat, testExp)
	b, err2 := Mint(key, "kid-1", "app_x_prod", testIat, testExp)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if a != b {
		t.Error("identical inputs produced different tokens")
	}
	c, _ := Mint(key, "kid-1", "app_y_prod", testIat, testExp)
	d, _ := Mint(key, "kid-1", "app_x_prod", testIat, testExp.Add(time.Second))
	if c == a || d == a {
		t.Error("different role or exp produced the same token")
	}
}

func TestMint_RejectsBadInput(t *testing.T) {
	key := newKey(t)
	cases := map[string]struct {
		f    func() (string, error)
		want string
	}{
		"nil key":           {func() (string, error) { return Mint(nil, "k", "r", testIat, testExp) }, "signing key"},
		"empty kid":         {func() (string, error) { return Mint(key, "", "r", testIat, testExp) }, "kid"},
		"empty role":        {func() (string, error) { return Mint(key, "k", "", testIat, testExp) }, "role"},
		"exp not after iat": {func() (string, error) { return Mint(key, "k", "r", testIat, testIat) }, "exp"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.f(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}
