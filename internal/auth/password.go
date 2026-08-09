package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var ErrInvalidPassword = errors.New("invalid password")

const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeySize = 32
	saltSize     = 16
)

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeySize)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func ValidatePassword(password string) error {
	if password == "" {
		return fmt.Errorf("%w: must not be empty", ErrInvalidPassword)
	}
	if len(password) > 1024 {
		return fmt.Errorf("%w: must contain at most 1024 bytes", ErrInvalidPassword)
	}
	return nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	parameters, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, parameters.time, parameters.memory, parameters.threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

type argonParameters struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parsePasswordHash(encoded string) (argonParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParameters{}, nil, nil, fmt.Errorf("invalid password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argonParameters{}, nil, nil, fmt.Errorf("unsupported password hash version")
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return argonParameters{}, nil, nil, fmt.Errorf("invalid password hash parameters")
	}
	if memory < 8*1024 || memory > 256*1024 || iterations == 0 || iterations > 10 || threads == 0 || threads > 8 {
		return argonParameters{}, nil, nil, fmt.Errorf("unsafe password hash parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return argonParameters{}, nil, nil, fmt.Errorf("invalid password hash salt")
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) < 16 || len(hash) > 64 {
		return argonParameters{}, nil, nil, fmt.Errorf("invalid password hash value")
	}
	return argonParameters{memory: memory, time: iterations, threads: threads}, salt, hash, nil
}
