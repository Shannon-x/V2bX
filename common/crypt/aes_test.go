package crypt

import (
	"crypto/aes"
	"encoding/base64"
	"strings"
	"testing"
)

// TestAesDecryptNoTrailingNUL is the W4.7 / audit #17 regression. The previous
// implementation allocated the output buffer using the base64-encoded length
// of the input string, which is ~4/3 of the decoded length — the surplus
// bytes were zero-initialised and returned verbatim in the returned string.
// A correct implementation must allocate based on the decoded byte length.
func TestAesDecryptNoTrailingNUL(t *testing.T) {
	key := []byte("0123456789ABCDEF") // 16-byte AES-128 key
	plain := []byte("AESBlockOf16Byte") // exactly aes.BlockSize == 16

	enc, err := AesEncrypt(plain, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	got, err := AesDecrypt(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if len(got) != aes.BlockSize {
		t.Fatalf("decrypt length = %d, want %d (would indicate base64-length allocation bug)", len(got), aes.BlockSize)
	}
	if strings.ContainsRune(got, 0) {
		t.Fatalf("decrypted string contains NUL padding: %q", got)
	}
	if got != string(plain) {
		t.Fatalf("decrypt = %q, want %q", got, plain)
	}
}

// TestAesDecryptShortBlockReturnsError checks that a too-short ciphertext
// gets a clean error instead of panicking inside a.Decrypt.
func TestAesDecryptShortBlockReturnsError(t *testing.T) {
	key := []byte("0123456789ABCDEF")
	// 8-byte payload base64-encoded — decoded length below block size.
	short := base64.StdEncoding.EncodeToString([]byte("12345678"))
	if _, err := AesDecrypt(short, key); err == nil {
		t.Fatalf("expected error for short ciphertext, got nil")
	}
}
