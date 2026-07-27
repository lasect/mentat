package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
	maxArgonMemory   = 64 * 1024
	maxArgonTime     = 10
	maxArgonThreads  = 16
	maxArgonSalt     = 64
	maxArgonKey      = 64
)

var ErrInvalidPasswordHash = errors.New("invalid encoded password hash")

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func ValidatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || length < 12 || length > 128 {
		return fmt.Errorf("%w: password must be between 12 and 128 characters", ErrInvalidInput)
	}
	return nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, ErrInvalidPasswordHash
	}
	var memory uint64
	var iterations uint64
	var parallelism uint64
	for _, field := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return false, ErrInvalidPasswordHash
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return false, fmt.Errorf("%w: invalid %q parameter", ErrInvalidPasswordHash, key)
		}
		switch key {
		case "m":
			memory = n
		case "t":
			iterations = n
		case "p":
			parallelism = n
		default:
			return false, fmt.Errorf("%w: unknown %q parameter", ErrInvalidPasswordHash, key)
		}
	}
	if memory == 0 || memory > maxArgonMemory ||
		iterations == 0 || iterations > maxArgonTime ||
		parallelism == 0 || parallelism > maxArgonThreads {
		return false, ErrInvalidPasswordHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 || len(salt) > maxArgonSalt {
		return false, fmt.Errorf("%w: invalid salt", ErrInvalidPasswordHash)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 || len(expected) > maxArgonKey {
		return false, fmt.Errorf("%w: invalid hash", ErrInvalidPasswordHash)
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
