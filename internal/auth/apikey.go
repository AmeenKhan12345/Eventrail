package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	keyLive      = "whk_live_"
	keyIDHex     = 8
	keySecretHex = 48
)

var errMalformedKey = errors.New("malformed api key")

// Generate builds a new API key with crypto/rand.
// The plaintext is shown once. prefix is the lookup id. hash is the SHA-256
// hex digest of the plaintext.
func Generate() (plaintext, prefix, hash string, err error) {
	var buf [keyIDHex/2 + keySecretHex/2]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", "", fmt.Errorf("generate api key: %w", err)
	}
	id := hex.EncodeToString(buf[:keyIDHex/2])
	secret := hex.EncodeToString(buf[keyIDHex/2:])
	prefix = keyLive + id
	plaintext = prefix + "_" + secret
	return plaintext, prefix, Hash(plaintext), nil
}

// Parse checks the exact key format and returns the lookup prefix
// "whk_live_" plus 8 hex characters. A bad key fails before any database call.
func Parse(key string) (string, error) {
	if len(key) != len(keyLive)+keyIDHex+1+keySecretHex {
		return "", errMalformedKey
	}
	if key[:len(keyLive)] != keyLive || key[len(keyLive)+keyIDHex] != '_' {
		return "", errMalformedKey
	}
	id := key[len(keyLive) : len(keyLive)+keyIDHex]
	secret := key[len(keyLive)+keyIDHex+1:]
	if !isLowerHex(id) || !isLowerHex(secret) {
		return "", errMalformedKey
	}
	return keyLive + id, nil
}

// Hash returns the SHA-256 hex digest of the full plaintext key.
func Hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func isLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
