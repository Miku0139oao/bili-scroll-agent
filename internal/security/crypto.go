package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type Cipher struct {
	key []byte
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("AES-256 key must be 32 bytes")
	}
	copyKey := append([]byte(nil), key...)
	return &Cipher{key: copyKey}, nil
}

func (c *Cipher) EncryptBytes(value []byte, aad string) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, value, []byte(aad))
	packed := append(nonce, sealed...)
	return "v1." + base64.URLEncoding.EncodeToString(packed), nil
}

func (c *Cipher) DecryptBytes(value, aad string) ([]byte, error) {
	if !strings.HasPrefix(value, "v1.") {
		return nil, errors.New("unsupported ciphertext version")
	}
	encoded := strings.TrimPrefix(value, "v1.")
	packed, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		packed, err = base64.RawURLEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(packed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("ciphertext is truncated")
	}
	plain, err := gcm.Open(nil, packed[:gcm.NonceSize()], packed[gcm.NonceSize():], []byte(aad))
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func (c *Cipher) EncryptJSON(value any, aad string) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return c.EncryptBytes(raw, aad)
}

func (c *Cipher) DecryptJSON(value, aad string, target any) error {
	raw, err := c.DecryptBytes(value, aad)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func (c *Cipher) TokenHash(token string) string {
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func NewSecret(prefix string, bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(raw), nil
}
