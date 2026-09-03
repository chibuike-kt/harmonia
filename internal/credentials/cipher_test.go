package credentials

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func randomBase64Key(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func TestNewCipher_InvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"not base64", "not-valid-base64!!"},
		{"too short", randomBase64Key(t, 16)},
		{"too long", randomBase64Key(t, 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if c, err := NewCipher(tt.key); err == nil {
				t.Fatalf("NewCipher(%q) = %+v, want error", tt.key, c)
			}
		})
	}
}

func TestCipher_EncryptDecryptRoundTrip(t *testing.T) {
	c, err := NewCipher(randomBase64Key(t, keyBytes))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	const plaintext = "sk-test-super-secret-key"
	ciphertext, nonce, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(ciphertext) == plaintext {
		t.Fatal("ciphertext must not equal plaintext")
	}

	got, err := c.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestCipher_EncryptUsesFreshNonce(t *testing.T) {
	c, err := NewCipher(randomBase64Key(t, keyBytes))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	_, nonce1, err := c.Encrypt("same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, nonce2, err := c.Encrypt("same-plaintext")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(nonce1) == string(nonce2) {
		t.Fatal("expected distinct nonces across calls")
	}
}

func TestCipher_DecryptRejectsTampering(t *testing.T) {
	c, err := NewCipher(randomBase64Key(t, keyBytes))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	ciphertext, nonce, err := c.Encrypt("sk-test-super-secret-key")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tampered := append([]byte{}, ciphertext...)
	tampered[0] ^= 0xFF
	if _, err := c.Decrypt(tampered, nonce); err == nil {
		t.Fatal("expected Decrypt to reject a tampered ciphertext")
	}

	otherKeyCipher, err := NewCipher(randomBase64Key(t, keyBytes))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if _, err := otherKeyCipher.Decrypt(ciphertext, nonce); err == nil {
		t.Fatal("expected Decrypt to fail under the wrong key")
	}
}
