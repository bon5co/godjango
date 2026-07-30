// Adapted from Django tests/auth_tests/test_tokens.py at commit 274a1d4.
// See THIRD_PARTY_NOTICES.md.
package auth_test

import (
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
)

func tokenGenerator(now time.Time) auth.ResetTokenGenerator {
	return auth.ResetTokenGenerator{
		Secret:  []byte("secret"),
		Timeout: 24 * time.Hour,
		Now:     func() time.Time { return now },
	}
}

func resetUser() *auth.User {
	return &auth.User{
		ID:           "user-1",
		Email:        "test@example.com",
		PasswordHash: "encoded-password",
	}
}

// Django: test_tokens.py::TokenGeneratorTest::test_make_token.
func TestPasswordResetTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	generator := tokenGenerator(now)
	user := resetUser()

	token, err := generator.Make(user)
	if err != nil {
		t.Fatalf("Make() error = %v", err)
	}
	if !generator.Check(user, token) {
		t.Error("Check() = false for fresh token")
	}
}

// Django: test_tokens.py::TokenGeneratorTest::test_timeout.
func TestPasswordResetTokenHasExactTimeoutBoundary(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	generator := tokenGenerator(now)
	user := resetUser()
	token, err := generator.Make(user)
	if err != nil {
		t.Fatalf("Make() error = %v", err)
	}

	atBoundary := tokenGenerator(now.Add(24 * time.Hour))
	if !atBoundary.Check(user, token) {
		t.Error("token invalid at exact timeout boundary")
	}
	afterBoundary := tokenGenerator(now.Add(24*time.Hour + time.Second))
	if afterBoundary.Check(user, token) {
		t.Error("token valid after timeout boundary")
	}
}

// Django: test_tokens.py::TokenGeneratorTest::test_token_with_different_email
// plus the password and last-login fields used by PasswordResetTokenGenerator.
func TestPasswordResetTokenInvalidatedByUserChanges(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mutate func(*auth.User)
	}{
		{
			name: "email",
			mutate: func(user *auth.User) {
				user.Email = "changed@example.com"
			},
		},
		{
			name: "password",
			mutate: func(user *auth.User) {
				user.PasswordHash = "changed-password"
			},
		},
		{
			name: "last login",
			mutate: func(user *auth.User) {
				lastLogin := now.Add(time.Minute)
				user.LastLogin = &lastLogin
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := tokenGenerator(now)
			user := resetUser()
			token, err := generator.Make(user)
			if err != nil {
				t.Fatalf("Make() error = %v", err)
			}
			test.mutate(user)
			if generator.Check(user, token) {
				t.Errorf("token remained valid after %s changed", test.name)
			}
		})
	}
}

// Django: test_tokens.py::TokenGeneratorTest::test_token_with_different_secret.
func TestPasswordResetTokenRejectsDifferentSecret(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	original := tokenGenerator(now)
	user := resetUser()
	token, err := original.Make(user)
	if err != nil {
		t.Fatalf("Make() error = %v", err)
	}

	different := tokenGenerator(now)
	different.Secret = []byte("different")
	if different.Check(user, token) {
		t.Error("token verified with a different secret")
	}
}

// Django: test_tokens.py::TokenGeneratorTest::test_check_token_secret_fallbacks.
func TestPasswordResetTokenAcceptsFallbackSecret(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	old := tokenGenerator(now)
	old.Secret = []byte("old-secret")
	user := resetUser()
	token, err := old.Make(user)
	if err != nil {
		t.Fatalf("Make() error = %v", err)
	}

	rotated := tokenGenerator(now)
	rotated.Secret = []byte("new-secret")
	rotated.FallbackSecrets = [][]byte{[]byte("old-secret")}
	if !rotated.Check(user, token) {
		t.Error("token signed with fallback secret was rejected")
	}
}

// Django: test_tokens.py::TokenGeneratorTest::test_check_token_with_nonexistent_token_and_user.
func TestPasswordResetTokenRejectsMissingInputs(t *testing.T) {
	generator := tokenGenerator(time.Now())
	user := resetUser()

	if generator.Check(nil, "token") {
		t.Error("Check(nil user) = true")
	}
	if generator.Check(user, "") {
		t.Error("Check(empty token) = true")
	}
}
