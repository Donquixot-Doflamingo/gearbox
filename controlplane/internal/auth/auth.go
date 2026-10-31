// Package auth holds credential primitives: random secrets, HMAC hashing of
// stored secrets, Ed25519 signing keys and the OIDC client.
package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// RandomString returns n random base62 characters.
func RandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// RandomID returns prefix + 12 random base62 characters.
func RandomID(prefix string) string { return prefix + RandomString(12) }

// Hasher hashes stored secrets (tokens, credentials, session ids) with
// HMAC-SHA256 under a server key, so a leaked database does not leak usable
// secrets.
type Hasher struct{ Key []byte }

// Hash returns the hex HMAC of s.
func (h Hasher) Hash(s string) string {
	m := hmac.New(sha256.New, h.Key)
	m.Write([]byte(s))
	return hex.EncodeToString(m.Sum(nil))
}

// Derive returns a sub-key for a purpose.
func (h Hasher) Derive(purpose string) []byte {
	m := hmac.New(sha256.New, h.Key)
	m.Write([]byte("gearbox:" + purpose))
	return m.Sum(nil)
}

// ParsePrivateKey reads an Ed25519 key from: a base64 32-byte seed or
// 64-byte private key, raw 32/64 bytes, or a PEM PKCS#8 "PRIVATE KEY".
func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	if blk, _ := pem.Decode(data); blk != nil {
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return nil, fmt.Errorf("signing key: %w", err)
		}
		pk, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("signing key: PEM key is not Ed25519")
		}
		return pk, nil
	}
	s := strings.TrimSpace(string(data))
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		data = raw
	} else if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		data = raw
	}
	switch len(data) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(data), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(data), nil
	}
	return nil, fmt.Errorf("signing key: want a 32-byte seed or 64-byte key, got %d bytes", len(data))
}

// LoadSigningKey reads GEARBOX_SIGNING_KEY: a file path or an inline base64
// seed.
func LoadSigningKey(v string) (ed25519.PrivateKey, error) {
	if b, err := os.ReadFile(v); err == nil {
		return ParsePrivateKey(b)
	}
	return ParsePrivateKey([]byte(v))
}

// LoadOrCreateKeyFile loads a base64 seed from path, creating it when
// missing (development mode). created reports whether it was generated.
func LoadOrCreateKeyFile(path string) (k ed25519.PrivateKey, created bool, err error) {
	if b, err := os.ReadFile(path); err == nil {
		k, err := ParsePrivateKey(b)
		return k, false, err
	}
	_, k, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k.Seed())+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	return k, true, nil
}

// TrustedKeyLine formats "id:base64(public key)" for GEARBOX_TRUSTED_KEYS.
func TrustedKeyLine(id string, k ed25519.PrivateKey) string {
	return id + ":" + base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
}

// LoadSecret reads a secret: a file path or the literal value.
func LoadSecret(v string) []byte {
	if b, err := os.ReadFile(v); err == nil {
		return []byte(strings.TrimSpace(string(b)))
	}
	return []byte(v)
}

// ---------------------------------------------------------------- OIDC

// OIDC is a configured OpenID Connect relying party.
type OIDC struct {
	verifier *oidc.IDTokenVerifier
	config   oauth2.Config
}

// Identity is what the control plane needs from an ID token.
type Identity struct {
	Subject string
	Email   string
	Name    string
}

// NewOIDC discovers the provider.
func NewOIDC(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*OIDC, error) {
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %s: %w", issuer, err)
	}
	return &OIDC{
		verifier: p.Verifier(&oidc.Config{ClientID: clientID}),
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
			Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
		},
	}, nil
}

// AuthURL returns the provider URL to redirect the browser to.
func (o *OIDC) AuthURL(state, nonce string) string {
	return o.config.AuthCodeURL(state, oidc.Nonce(nonce))
}

// Exchange trades the code for a verified identity.
func (o *OIDC) Exchange(ctx context.Context, code, nonce string) (Identity, error) {
	tok, err := o.config.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc: exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return Identity{}, errors.New("oidc: no id_token in response")
	}
	idt, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc: verify: %w", err)
	}
	if idt.Nonce != nonce {
		return Identity{}, errors.New("oidc: nonce mismatch")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, err
	}
	if claims.Email == "" {
		return Identity{}, errors.New("oidc: id token has no email claim")
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return Identity{}, errors.New("oidc: email not verified")
	}
	return Identity{Subject: idt.Subject, Email: claims.Email, Name: claims.Name}, nil
}
