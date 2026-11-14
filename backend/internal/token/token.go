// Package token generates and verifies API tokens. Only the SHA-256 of a token is
// stored (in the configuration, as code); the token itself is shown once.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

// Prefix makes tokens recognizable by secret scanners.
const Prefix = "kbj_"

// New returns a random token and the hash to put in the configuration.
func New() (tok, hash string) {
	b := make([]byte, 24)
	rand.Read(b)
	tok = Prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	return tok, Hash(tok)
}

// Hash returns "sha256:<hex>".
func Hash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Matches compares a presented token with a stored hash in constant time.
func Matches(tok, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(tok)), []byte(hash)) == 1
}
