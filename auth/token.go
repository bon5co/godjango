package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// ResetTokenGenerator creates single-purpose, expiring password reset tokens.
type ResetTokenGenerator struct {
	Secret          []byte
	FallbackSecrets [][]byte
	Timeout         time.Duration
	Now             func() time.Time
}

func (g ResetTokenGenerator) Make(user *User) (string, error) {
	if user == nil || len(g.Secret) == 0 {
		return "", ErrInvalidResetTokenInput
	}
	timestamp := g.now().UTC().UnixNano()
	encodedTimestamp := strconv.FormatInt(timestamp, 36)
	signature := g.signature(user, timestamp, g.Secret)
	return encodedTimestamp + "-" + signature, nil
}

func (g ResetTokenGenerator) Check(user *User, token string) bool {
	if user == nil || token == "" || len(g.Secret) == 0 {
		return false
	}
	encodedTimestamp, signature, ok := strings.Cut(token, "-")
	if !ok || encodedTimestamp == "" || signature == "" {
		return false
	}
	timestamp, err := strconv.ParseInt(encodedTimestamp, 36, 64)
	if err != nil {
		return false
	}
	issuedAt := time.Unix(0, timestamp).UTC()
	now := g.now().UTC()
	if now.Before(issuedAt) || now.Sub(issuedAt) > g.Timeout {
		return false
	}

	secrets := make([][]byte, 0, 1+len(g.FallbackSecrets))
	secrets = append(secrets, g.Secret)
	secrets = append(secrets, g.FallbackSecrets...)
	for _, secret := range secrets {
		expected := g.signature(user, timestamp, secret)
		if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

func (g ResetTokenGenerator) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g ResetTokenGenerator) signature(user *User, timestamp int64, secret []byte) string {
	lastLogin := ""
	if user.LastLogin != nil {
		lastLogin = user.LastLogin.UTC().Format(time.RFC3339Nano)
	}
	value := strings.Join([]string{
		user.ID,
		user.PasswordHash,
		lastLogin,
		strconv.FormatInt(timestamp, 10),
		user.Email,
	}, "\x00")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
