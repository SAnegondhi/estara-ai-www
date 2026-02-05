package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"strings"
)

// DecryptPassword decrypts a password encrypted with AES-256-GCM
// Input format: base64(iv):base64(authTag):base64(ciphertext)
func DecryptPassword(encrypted string, keyHex string) (string, error) {
	// Parse key from hex
	key, err := hexDecode(keyHex)
	if err != nil {
		return "", errors.New("invalid encryption key format")
	}
	if len(key) != 32 {
		return "", errors.New("encryption key must be 32 bytes (64 hex characters)")
	}

	// Split the encrypted string
	parts := strings.Split(encrypted, ":")
	if len(parts) != 3 {
		return "", errors.New("invalid encrypted data format")
	}

	iv, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("invalid IV encoding")
	}

	authTag, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid auth tag encoding")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("invalid ciphertext encoding")
	}

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// Append auth tag to ciphertext (Go's GCM expects it appended)
	ciphertextWithTag := append(ciphertext, authTag...)

	// Decrypt
	plaintext, err := gcm.Open(nil, iv, ciphertextWithTag, nil)
	if err != nil {
		return "", errors.New("decryption failed: " + err.Error())
	}

	return string(plaintext), nil
}

// hexDecode decodes a hex string to bytes
func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("hex string must have even length")
	}

	result := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var b byte
		for j := 0; j < 2; j++ {
			c := s[i+j]
			var val byte
			switch {
			case c >= '0' && c <= '9':
				val = c - '0'
			case c >= 'a' && c <= 'f':
				val = c - 'a' + 10
			case c >= 'A' && c <= 'F':
				val = c - 'A' + 10
			default:
				return nil, errors.New("invalid hex character")
			}
			b = b<<4 | val
		}
		result[i/2] = b
	}
	return result, nil
}
