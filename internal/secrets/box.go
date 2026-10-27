package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

const (
	keySize    = 32
	secretSize = 32
	version    = 0x01
)

// Box encrypts signing secrets with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// NewBox checks that key is 32 bytes and prepares AES-256-GCM.
func NewBox(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// GenerateSecret returns "whsec_" plus standard base64 of 32 random bytes.
func (b *Box) GenerateSecret() (string, error) {
	buf := make([]byte, secretSize)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(buf), nil
}

// Encrypt seals plaintext. The blob is version || nonce || ciphertext-with-tag.
// Each call uses a new 12-byte nonce.
func (b *Box) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	sealed := b.aead.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 1+len(nonce)+len(sealed))
	out[0] = version
	copy(out[1:], nonce)
	copy(out[1+len(nonce):], sealed)
	return out, nil
}

// Decrypt opens a blob produced by Encrypt. A wrong key, a bad version, or a
// changed byte returns an error.
func (b *Box) Decrypt(blob []byte) (string, error) {
	nonceSize := b.aead.NonceSize()
	if len(blob) < 1+nonceSize+b.aead.Overhead() {
		return "", fmt.Errorf("ciphertext too short")
	}
	if blob[0] != version {
		return "", fmt.Errorf("unknown ciphertext version")
	}
	nonce := blob[1 : 1+nonceSize]
	ciphertext := blob[1+nonceSize:]
	plain, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}
