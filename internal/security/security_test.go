package security

import (
	"testing"
	"time"
)

func TestPasswordRoundTripAndMinimumLength(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("expected short password to be rejected")
	}
	hash, err := HashPassword("a correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "a correct horse battery staple") {
		t.Fatal("valid password was rejected")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("wrong password was accepted")
	}
}

func TestCipherRoundTripAndTokenHashing(t *testing.T) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	cipher, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.EncryptBytes([]byte("secret"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "secret" {
		t.Fatal("ciphertext was not encrypted")
	}
	plain, err := cipher.DecryptBytes(sealed, "test")
	if err != nil || string(plain) != "secret" {
		t.Fatalf("decrypt failed: %v %q", err, plain)
	}
	if _, err := cipher.DecryptBytes(sealed, "different-aad"); err == nil {
		t.Fatal("AAD mismatch should fail")
	}
	if cipher.TokenHash("a") == cipher.TokenHash("b") {
		t.Fatal("token hashes should differ")
	}
}

func TestTOTPCodeAndWindow(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	code := TOTPCode(secret, now)
	if len(code) != 6 {
		t.Fatalf("unexpected code %q", code)
	}
	if !VerifyTOTP(secret, code, now) {
		t.Fatal("current TOTP code was rejected")
	}
	if VerifyTOTP(secret, code, now.Add(90*time.Second)) {
		t.Fatal("expired TOTP code was accepted")
	}
}
