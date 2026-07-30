package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	passwordAlgorithm  = "pbkdf2_sha256"
	passwordIterations = 1_800_000
)

// PasswordCheck is the result of verifying an encoded password.
type PasswordCheck struct {
	OK          bool
	NeedsUpdate bool
}

// PasswordHasher stores and verifies Django-compatible encoded passwords.
type PasswordHasher struct{}

func NewPasswordHasher() *PasswordHasher {
	return &PasswordHasher{}
}

func (h *PasswordHasher) Encode(raw *string) (string, error) {
	if raw == nil {
		return "!" + rand.Text(), nil
	}

	salt := rand.Text()
	derived, err := pbkdf2.Key(sha256.New, *raw, []byte(salt), passwordIterations, sha256.Size)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}
	return fmt.Sprintf(
		"%s$%d$%s$%s",
		passwordAlgorithm,
		passwordIterations,
		salt,
		base64.StdEncoding.EncodeToString(derived),
	), nil
}

func (h *PasswordHasher) Check(raw *string, encoded string) (PasswordCheck, error) {
	if strings.HasPrefix(encoded, "!") {
		return PasswordCheck{}, nil
	}

	parts := strings.Split(encoded, "$")
	if len(parts) == 0 || parts[0] != passwordAlgorithm {
		return PasswordCheck{}, ErrUnknownPasswordAlgorithm
	}
	if len(parts) != 4 {
		return PasswordCheck{}, ErrInvalidPasswordEncoding
	}

	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return PasswordCheck{}, ErrInvalidPasswordEncoding
	}
	expected, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return PasswordCheck{}, ErrInvalidPasswordEncoding
	}
	if raw == nil {
		return PasswordCheck{}, nil
	}

	derived, err := pbkdf2.Key(sha256.New, *raw, []byte(parts[2]), iterations, len(expected))
	if err != nil {
		return PasswordCheck{}, fmt.Errorf("derive password hash: %w", err)
	}
	ok := subtle.ConstantTimeCompare(derived, expected) == 1
	return PasswordCheck{
		OK:          ok,
		NeedsUpdate: ok && iterations != passwordIterations,
	}, nil
}

func (h *PasswordHasher) IsUsable(encoded string) bool {
	return encoded != "" && !strings.HasPrefix(encoded, "!")
}
