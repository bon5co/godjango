// Adapted from Django tests/auth_tests/test_models.py at commit 274a1d4.
// See THIRD_PARTY_NOTICES.md.
package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bon5co/godjango/auth"
)

type memoryUserStore struct {
	users           map[string]*auth.User
	passwordUpdates int
}

func newMemoryUserStore(users ...*auth.User) *memoryUserStore {
	store := &memoryUserStore{users: make(map[string]*auth.User)}
	for _, user := range users {
		store.users[user.Username] = user
	}
	return store
}

func (s *memoryUserStore) InsertUser(_ context.Context, user *auth.User) error {
	if _, exists := s.users[user.Username]; exists {
		return errors.New("duplicate username")
	}
	s.users[user.Username] = user
	return nil
}

func (s *memoryUserStore) UserByUsername(_ context.Context, username string) (*auth.User, error) {
	user, ok := s.users[username]
	if !ok {
		return nil, auth.ErrUserNotFound
	}
	return user, nil
}

func (s *memoryUserStore) UserByID(_ context.Context, id string) (*auth.User, error) {
	for _, user := range s.users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, auth.ErrUserNotFound
}

func (s *memoryUserStore) UsersByEmail(_ context.Context, email string) ([]*auth.User, error) {
	var users []*auth.User
	for _, user := range s.users {
		if user.Email == email {
			users = append(users, user)
		}
	}
	return users, nil
}

func (s *memoryUserStore) UpdatePassword(_ context.Context, user *auth.User) error {
	if _, ok := s.users[user.Username]; !ok {
		return auth.ErrUserNotFound
	}
	s.users[user.Username] = user
	s.passwordUpdates++
	return nil
}

// Django: test_models.py::UserManagerTestCase::{
// test_create_user_email_domain_normalize,
// test_create_user_email_domain_normalize_rfc3696,
// test_create_user_email_domain_normalize_with_whitespace}.
func TestNormalizeEmailLowercasesOnlyDomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "ordinary domain",
			input: "normal@DOMAIN.COM",
			want:  "normal@domain.com",
		},
		{
			name:  "escaped at sign in local part",
			input: `Abc\@DEF@EXAMPLE.com`,
			want:  `Abc\@DEF@example.com`,
		},
		{
			name:  "whitespace remains untouched",
			input: `email\ with_whitespace@D.COM`,
			want:  `email\ with_whitespace@d.com`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := auth.NormalizeEmail(test.input); got != test.want {
				t.Fatalf("NormalizeEmail(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// Django: test_models.py::AbstractBaseUserTests::test_clean_normalize_username.
func TestNormalizeUsernameUsesUnicodeNFKC(t *testing.T) {
	const input = "iamtheΩ"
	const want = "iamtheΩ"

	if got := auth.NormalizeUsername(input); got != want {
		t.Fatalf("NormalizeUsername(%q) = %q, want %q", input, got, want)
	}
}

// Django: test_models.py::UserManagerTestCase::test_empty_username.
func TestCreateUserRequiresUsername(t *testing.T) {
	manager := auth.NewManager(newMemoryUserStore(), auth.NewPasswordHasher())

	_, err := manager.CreateUser(context.Background(), auth.CreateUserOptions{})
	if !errors.Is(err, auth.ErrUsernameRequired) {
		t.Fatalf("CreateUser() error = %v, want %v", err, auth.ErrUsernameRequired)
	}
}

// Django: test_models.py::UserManagerTestCase::test_create_user and
// test_models.py::IsActiveTestCase::test_is_active_field_default.
func TestCreateUserNormalizesEmailAndDefaultsActive(t *testing.T) {
	store := newMemoryUserStore()
	manager := auth.NewManager(store, auth.NewPasswordHasher())

	user, err := manager.CreateUser(context.Background(), auth.CreateUserOptions{
		Username: "user",
		Email:    "CaseSensitive@EXAMPLE.COM",
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.Username != "user" {
		t.Errorf("Username = %q, want user", user.Username)
	}
	if user.Email != "CaseSensitive@example.com" {
		t.Errorf("Email = %q, want CaseSensitive@example.com", user.Email)
	}
	if !user.IsActive {
		t.Error("IsActive = false, want true")
	}
	if user.HasUsablePassword(auth.NewPasswordHasher()) {
		t.Error("user created without a password has a usable password")
	}
	if stored, ok := store.users["user"]; !ok || stored != user {
		t.Error("created user was not inserted into UserStore")
	}
}

// Django: test_models.py::UserManagerTestCase::{
// test_create_superuser_raises_error_on_false_is_staff,
// test_create_super_user_raises_error_on_false_is_superuser}.
func TestCreateSuperuserRequiresStaffAndSuperuserFlags(t *testing.T) {
	manager := auth.NewManager(newMemoryUserStore(), auth.NewPasswordHasher())
	password := "test"

	tests := []struct {
		name string
		opts auth.CreateUserOptions
		want error
	}{
		{
			name: "staff false",
			opts: auth.CreateUserOptions{
				Username:    "root",
				Password:    &password,
				IsStaff:     false,
				IsSuperuser: true,
			},
			want: auth.ErrSuperuserNotStaff,
		},
		{
			name: "superuser false",
			opts: auth.CreateUserOptions{
				Username:    "root",
				Password:    &password,
				IsStaff:     true,
				IsSuperuser: false,
			},
			want: auth.ErrSuperuserFlag,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := manager.CreateSuperuser(context.Background(), test.opts)
			if !errors.Is(err, test.want) {
				t.Fatalf("CreateSuperuser() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestChangePasswordUsesStoreAndInvalidatesOldPassword(t *testing.T) {
	oldPassword := "old-secret"
	store := newMemoryUserStore()
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	user, err := manager.CreateUser(context.Background(), auth.CreateUserOptions{
		Username: "alice",
		Password: &oldPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	newPassword := "new-secret"

	if err := manager.ChangePassword(context.Background(), "alice", newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if store.passwordUpdates != 1 {
		t.Fatalf("password updates = %d, want 1", store.passwordUpdates)
	}
	if ok, err := user.CheckPassword(auth.NewPasswordHasher(), oldPassword); err != nil || ok {
		t.Fatalf("old password check = %v, %v; want false, nil", ok, err)
	}
	if ok, err := user.CheckPassword(auth.NewPasswordHasher(), newPassword); err != nil || !ok {
		t.Fatalf("new password check = %v, %v; want true, nil", ok, err)
	}
}

// Django: test_auth_backends.py::ModelBackendTest::test_authenticate_inactive.
func TestAuthenticateRejectsInactiveUser(t *testing.T) {
	// Encoded by Django's PBKDF2 hasher for "lètmein" with a fixed salt.
	user := &auth.User{
		ID:           "inactive",
		Username:     "inactive",
		IsActive:     false,
		PasswordHash: "pbkdf2_sha256$1800000$seasalt$sXv9FzN4gEo6/P8G5H1jvir9BIb5e5EkXoVGyjOniNE=",
	}
	manager := auth.NewManager(newMemoryUserStore(user), auth.NewPasswordHasher())

	got, err := manager.Authenticate(context.Background(), "inactive", "lètmein")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got != nil {
		t.Fatalf("Authenticate() = %#v, want nil inactive user", got)
	}
}

// Django: test_auth_backends.py::BaseModelBackendTest::{
// test_authentication_timing,test_authentication_without_credentials}.
func TestAuthenticateAcceptsActiveUserAndRejectsBadCredentials(t *testing.T) {
	user := &auth.User{
		ID:           "active",
		Username:     "active",
		IsActive:     true,
		PasswordHash: "pbkdf2_sha256$1800000$seasalt$sXv9FzN4gEo6/P8G5H1jvir9BIb5e5EkXoVGyjOniNE=",
	}
	manager := auth.NewManager(newMemoryUserStore(user), auth.NewPasswordHasher())

	got, err := manager.Authenticate(context.Background(), "active", "lètmein")
	if err != nil {
		t.Fatalf("Authenticate(correct) error = %v", err)
	}
	if got != user {
		t.Fatalf("Authenticate(correct) = %#v, want %#v", got, user)
	}

	for name, credentials := range map[string][2]string{
		"wrong password": {"active", "wrong"},
		"missing user":   {"missing", "lètmein"},
	} {
		t.Run(strings.ReplaceAll(name, " ", "_"), func(t *testing.T) {
			got, err := manager.Authenticate(
				context.Background(),
				credentials[0],
				credentials[1],
			)
			if err != nil {
				t.Fatalf("Authenticate() error = %v", err)
			}
			if got != nil {
				t.Fatalf("Authenticate() = %#v, want nil", got)
			}
		})
	}
}
