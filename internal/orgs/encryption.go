package orgs

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

func NewConnectionCipher(key []byte, keyVersion int16) (*ConnectionCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("connection encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create connection cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create connection AEAD: %w", err)
	}
	if keyVersion <= 0 {
		return nil, fmt.Errorf("connection encryption key version must be positive")
	}
	return &ConnectionCipher{aead: aead, keyVersion: keyVersion}, nil
}

func (c *ConnectionCipher) Encrypt(plaintext string, associatedData []byte) (ciphertext, nonce []byte, keyVersion int16, err error) {
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, 0, fmt.Errorf("generate connection nonce: %w", err)
	}
	ciphertext = c.aead.Seal(nil, nonce, []byte(plaintext), associatedData)
	return ciphertext, nonce, c.keyVersion, nil
}

func (c *ConnectionCipher) Decrypt(ciphertext, nonce, associatedData []byte, keyVersion int16) (string, error) {
	if keyVersion != c.keyVersion {
		return "", fmt.Errorf("unsupported connection encryption key version %d", keyVersion)
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return "", fmt.Errorf("decrypt database connection: %w", err)
	}
	return string(plaintext), nil
}
