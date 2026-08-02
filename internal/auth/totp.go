package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	totpPeriod = int64(30)
	totpDigits = 6
)

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

func NewTOTPSecret() (string, error) {
	value := make([]byte, 20)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate TOTP secret: %w", err)
	}
	return base32NoPadding.EncodeToString(value), nil
}

func TOTPURI(secret, account string) (string, error) {
	if _, err := decodeTOTPSecret(secret); err != nil {
		return "", err
	}
	if strings.TrimSpace(account) == "" {
		return "", fmt.Errorf("TOTP account is required")
	}
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", "Relayward")
	query.Set("algorithm", "SHA1")
	query.Set("digits", strconv.Itoa(totpDigits))
	query.Set("period", strconv.FormatInt(totpPeriod, 10))
	return "otpauth://totp/" + url.PathEscape("Relayward:"+account) + "?" + query.Encode(), nil
}

func VerifyTOTP(secret, code string, now time.Time) bool {
	_, valid := MatchTOTP(secret, code, now)
	return valid
}

func MatchTOTP(secret, code string, now time.Time) (int64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return 0, false
	}
	counter := now.UTC().Unix() / totpPeriod
	for offset := int64(-1); offset <= 1; offset++ {
		expected := totpCode(key, counter+offset)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return counter + offset, true
		}
	}
	return 0, false
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	value, err := base32NoPadding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(value) != 20 {
		return nil, fmt.Errorf("invalid TOTP secret")
	}
	return value, nil
}

func totpCode(key []byte, counter int64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(counter))
	digest := hmac.New(sha1.New, key)
	_, _ = digest.Write(message[:])
	sum := digest.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
