package sheets

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// MaxKeyBytes bounds the service account JSON key.
const MaxKeyBytes = 16 << 10

// Fixed Google endpoints and scope. The token_uri of a key is ignored on
// purpose: a forged key must not send a signed assertion elsewhere.
const (
	TokenURL = "https://oauth2.googleapis.com/token"
	APIBase  = "https://sheets.googleapis.com/v4/spreadsheets/"
	Scope    = "https://www.googleapis.com/auth/spreadsheets"

	TokenHost = "oauth2.googleapis.com"
	APIHost   = "sheets.googleapis.com"
)

// Account is a parsed service account key.
type Account struct {
	Email string
	KeyID string
	key   *rsa.PrivateKey
}

// keyFile is the subset of the JSON key file Google issues.
type keyFile struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
}

// ParseServiceAccount checks a JSON key of a Google service account.
// Error messages never quote the key.
func ParseServiceAccount(raw []byte) (Account, error) {
	if len(raw) == 0 {
		return Account{}, errors.New("the key is empty: paste the whole JSON file downloaded from Google Cloud")
	}
	if len(raw) > MaxKeyBytes {
		return Account{}, errors.New("the key is larger than 16 KB: paste only the JSON key file")
	}
	var f keyFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Account{}, errors.New("the key is not valid JSON: paste the whole JSON file downloaded from Google Cloud")
	}
	if f.Type != "service_account" {
		return Account{}, errors.New(`this is not a service account key: "type" must be "service_account"`)
	}
	addr, err := mail.ParseAddress(f.ClientEmail)
	if err != nil || addr.Address != f.ClientEmail || strings.ContainsAny(f.ClientEmail, " <>") {
		return Account{}, errors.New(`the key has no valid "client_email"`)
	}
	key, err := parseRSAKey(f.PrivateKey)
	if err != nil {
		return Account{}, err
	}
	return Account{Email: f.ClientEmail, KeyID: f.PrivateKeyID, key: key}, nil
}

func parseRSAKey(p string) (*rsa.PrivateKey, error) {
	errKey := errors.New(`the key has no valid "private_key" (an RSA key in PEM format)`)
	block, _ := pem.Decode([]byte(p))
	if block == nil {
		return nil, errKey
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errKey
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errKey
}

// assertion builds the signed JWT exchanged for an access token.
func (a Account) assertion(now time.Time) (string, error) {
	if a.key == nil {
		return "", errors.New("service account key is not loaded")
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if a.KeyID != "" {
		header["kid"] = a.KeyID
	}
	claims := map[string]any{
		"iss": a.Email, "scope": Scope, "aud": TokenURL,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	h, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("encode jwt header: %w", err)
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode jwt claims: %w", err)
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
