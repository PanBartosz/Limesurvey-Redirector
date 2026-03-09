package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

type Protector struct {
	aead cipher.AEAD
}

func NewProtector(secret string) (*Protector, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("credentials secret must be at least 32 characters")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Protector{aead: aead}, nil
}

func (p *Protector) Encrypt(plaintext string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("credentials protector is not configured")
	}
	if plaintext == "" {
		return "", fmt.Errorf("credentials value is required")
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := p.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (p *Protector) Decrypt(ciphertext string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("credentials protector is not configured")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		return "", fmt.Errorf("decode encrypted credentials: %w", err)
	}
	if len(raw) < p.aead.NonceSize() {
		return "", fmt.Errorf("encrypted credentials payload is invalid")
	}
	nonce := raw[:p.aead.NonceSize()]
	payload := raw[p.aead.NonceSize():]
	plaintext, err := p.aead.Open(nil, nonce, payload, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}
	return string(plaintext), nil
}
