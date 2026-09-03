// Package credentials implements Phase 2's BYOK provider credential
// store: verify a user-supplied provider API key with a live call, then
// encrypt and persist it. See ADR-002 for the reasoning.
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// keyBytes is the AES-256 key size — 32 bytes, not negotiable.
const keyBytes = 32

// Cipher encrypts and decrypts BYOK provider keys at rest with
// AES-256-GCM, the same primitive and discipline as this codebase's
// existing custodial-wallet encryption: encrypt at rest, decrypt only in
// memory at call time, never log plaintext. Everything here is
// crypto/aes and crypto/cipher directly — no hand-rolled cipher mode,
// padding, or key derivation; JWT-style bugs in DIY authenticated
// encryption are exactly the class of mistake AES-GCM's stdlib
// implementation exists to prevent.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a base64-encoded 32-byte AES-256 key
// (generate one with `openssl rand -base64 32`). Fails loudly on a
// missing, malformed, or wrong-length key rather than silently
// accepting something weaker — callers should treat a non-nil error as
// "credential encryption is not configured" and refuse to store
// anything, the same way GoogleConfig/GitHubConfig fail cleanly on
// missing OAuth settings instead of sending a broken request.
func NewCipher(base64Key string) (*Cipher, error) {
	if base64Key == "" {
		return nil, errors.New("credentials: encryption key is not configured")
	}
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("credentials: decode encryption key: %w", err)
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("credentials: encryption key must be %d bytes, got %d", keyBytes, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credentials: build aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credentials: build gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext under a fresh, randomly generated nonce.
// Both the ciphertext and the nonce must be stored — the nonce isn't
// secret, but is required to decrypt, and GCM's security guarantee
// depends on never reusing one under the same key, so a new one comes
// from crypto/rand on every call rather than being derived or reused.
func (c *Cipher) Encrypt(plaintext string) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("credentials: generate nonce: %w", err)
	}
	ciphertext = c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// Decrypt reverses Encrypt. GCM authenticates as well as encrypts, so a
// tampered or mismatched ciphertext/nonce pair fails here rather than
// silently returning garbage.
func (c *Cipher) Decrypt(ciphertext, nonce []byte) (string, error) {
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("credentials: decrypt: %w", err)
	}
	return string(plaintext), nil
}
