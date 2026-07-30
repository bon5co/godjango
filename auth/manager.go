package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

// NormalizeEmail preserves the local part and lowercases the domain.
func NormalizeEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return email
	}
	return email[:at+1] + strings.ToLower(email[at+1:])
}

// NormalizeUsername applies Unicode NFKC normalization.
func NormalizeUsername(username string) string {
	return norm.NFKC.String(username)
}

// CreateUserOptions is shared by normal and superuser creation.
type CreateUserOptions struct {
	Username    string
	Email       string
	Password    *string
	IsStaff     bool
	IsActive    *bool
	IsSuperuser bool
}

// UserStore is the persistence boundary used by Manager. The default
// implementation is Bun-backed; unit tests can use a deterministic store.
type UserStore interface {
	InsertUser(ctx context.Context, user *User) error
	UserByUsername(ctx context.Context, username string) (*User, error)
}

// PasswordUpdater is the optional persistence capability required by
// changepassword. The default BunStore implements it.
type PasswordUpdater interface {
	UpdatePassword(ctx context.Context, user *User) error
}

type UserByIDStore interface {
	UserByID(ctx context.Context, id string) (*User, error)
}

type UsersByEmailStore interface {
	UsersByEmail(ctx context.Context, email string) ([]*User, error)
}

// Manager owns user behavior over a replaceable persistence boundary.
type Manager struct {
	store  UserStore
	hasher *PasswordHasher
}

func NewManager(store UserStore, hasher *PasswordHasher) *Manager {
	return &Manager{store: store, hasher: hasher}
}

func (m *Manager) CreateUser(ctx context.Context, opts CreateUserOptions) (*User, error) {
	username := NormalizeUsername(opts.Username)
	if username == "" {
		return nil, ErrUsernameRequired
	}

	active := true
	if opts.IsActive != nil {
		active = *opts.IsActive
	}
	user := &User{
		Username:    username,
		Email:       NormalizeEmail(opts.Email),
		IsStaff:     opts.IsStaff,
		IsActive:    active,
		IsSuperuser: opts.IsSuperuser,
		DateJoined:  time.Now().UTC(),
	}
	if err := user.SetPassword(m.hasher, opts.Password); err != nil {
		return nil, err
	}
	if err := m.store.InsertUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (m *Manager) CreateSuperuser(ctx context.Context, opts CreateUserOptions) (*User, error) {
	if !opts.IsStaff {
		return nil, ErrSuperuserNotStaff
	}
	if !opts.IsSuperuser {
		return nil, ErrSuperuserFlag
	}
	return m.CreateUser(ctx, opts)
}

func (m *Manager) ChangePassword(ctx context.Context, username, password string) error {
	user, err := m.store.UserByUsername(ctx, NormalizeUsername(username))
	if err != nil {
		return err
	}
	return m.SetPassword(ctx, user, password)
}

func (m *Manager) SetPassword(ctx context.Context, user *User, password string) error {
	updater, ok := m.store.(PasswordUpdater)
	if !ok {
		return errors.New("godjango auth: user store does not support password updates")
	}
	if err := user.SetPassword(m.hasher, &password); err != nil {
		return err
	}
	return updater.UpdatePassword(ctx, user)
}

func (m *Manager) UserByID(ctx context.Context, id string) (*User, error) {
	store, ok := m.store.(UserByIDStore)
	if !ok {
		return nil, errors.New("godjango auth: user store does not support ID lookup")
	}
	return store.UserByID(ctx, id)
}

func (m *Manager) UsersByEmail(ctx context.Context, email string) ([]*User, error) {
	store, ok := m.store.(UsersByEmailStore)
	if !ok {
		return nil, errors.New("godjango auth: user store does not support email lookup")
	}
	return store.UsersByEmail(ctx, NormalizeEmail(email))
}

func (m *Manager) Authenticate(ctx context.Context, username, password string) (*User, error) {
	user, err := m.store.UserByUsername(ctx, NormalizeUsername(username))
	if errors.Is(err, ErrUserNotFound) {
		// Perform one full password hash for missing users to reduce username
		// enumeration through response timing.
		_, hashErr := m.hasher.Encode(&password)
		if hashErr != nil {
			return nil, hashErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ok, err := user.CheckPassword(m.hasher, password)
	if err != nil {
		return nil, err
	}
	if !ok || !user.IsActive {
		return nil, nil
	}
	return user, nil
}
