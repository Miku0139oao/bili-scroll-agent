package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func NewTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func TOTPURI(secret, username, issuer string) string {
	label := url.PathEscape(issuer + ":" + username)
	query := url.Values{
		"secret":    []string{secret},
		"issuer":    []string{issuer},
		"algorithm": []string{"SHA1"},
		"digits":    []string{"6"},
		"period":    []string{"30"},
	}
	return "otpauth://totp/" + label + "?" + query.Encode()
}

func VerifyTOTP(secret, code string, now time.Time) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return false
	}
	counter := now.Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		if hmac.Equal([]byte(TOTPCode(secret, time.Unix((counter+offset)*30, 0))), []byte(code)) {
			return true
		}
	}
	return false
}

func TOTPCode(secret string, now time.Time) string {
	counter := uint64(now.Unix() / 30)
	normalized := strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	padding := (8 - len(normalized)%8) % 8
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(
		normalized + strings.Repeat("=", padding),
	)
	if err != nil {
		return ""
	}
	buffer := make([]byte, 8)
	binary.BigEndian.PutUint64(buffer, counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buffer)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
