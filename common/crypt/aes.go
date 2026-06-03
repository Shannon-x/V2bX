package crypt

import (
	"crypto/aes"
	"encoding/base64"
	"fmt"
)

func AesEncrypt(data []byte, key []byte) (string, error) {
	a, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(data) < aes.BlockSize {
		return "", fmt.Errorf("aes encrypt: input %d bytes < block size %d", len(data), aes.BlockSize)
	}
	en := make([]byte, len(data))
	a.Encrypt(en, data)
	return base64.StdEncoding.EncodeToString(en), nil
}

func AesDecrypt(data string, key []byte) (string, error) {
	d, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	a, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	// W4.7 / audit #17: previously allocated `len(data)` bytes — i.e. the
	// base64-encoded length, which is ~4/3 of the decoded length and always
	// pads with NUL bytes returned to the caller as part of the string.
	// We must allocate based on the decoded length and refuse blocks
	// shorter than aes.BlockSize (otherwise a.Decrypt would panic).
	if len(d) < aes.BlockSize {
		return "", fmt.Errorf("aes decrypt: decoded ciphertext %d bytes < block size %d", len(d), aes.BlockSize)
	}
	de := make([]byte, len(d))
	a.Decrypt(de, d)
	return string(de), nil
}
