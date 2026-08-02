package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strings"
)

const recoveryAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

func NewRecoveryCodes(count int) ([]string, error) {
	if count < 1 || count > 20 {
		return nil, fmt.Errorf("recovery code count must be between 1 and 20")
	}
	codes := make([]string, 0, count)
	seen := make(map[string]struct{}, count)
	for len(codes) < count {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}

func RecoveryCodeHash(code string) []byte {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	value := sha256.Sum256([]byte(normalized))
	return value[:]
}

func newRecoveryCode() (string, error) {
	const symbols = 16
	value := make([]byte, symbols)
	buffer := make([]byte, symbols*2)
	for index := 0; index < symbols; {
		if _, err := rand.Read(buffer); err != nil {
			return "", fmt.Errorf("generate recovery code: %w", err)
		}
		for _, candidate := range buffer {
			limit := 256 - (256 % len(recoveryAlphabet))
			if int(candidate) >= limit {
				continue
			}
			value[index] = recoveryAlphabet[int(candidate)%len(recoveryAlphabet)]
			index++
			if index == symbols {
				break
			}
		}
	}
	return fmt.Sprintf("%s-%s-%s-%s", value[0:4], value[4:8], value[8:12], value[12:16]), nil
}
