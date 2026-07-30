package auth

import (
	"context"
	"errors"
	"io/fs"
	"testing/fstest"

	"github.com/bon5co/godjango/database"
)

var ErrPersistenceNotImplemented = errors.New("godjango auth persistence: not implemented")

type appConfig struct{}

func (appConfig) Name() string { return "auth" }

func (appConfig) MigrationFS() fs.FS { return fstest.MapFS{} }

// App registers default auth migrations with a project.
var App appConfig

// BunStore is GoDjangGo's default PostgreSQL auth store.
type BunStore struct {
	db *database.DB
}

func NewBunStore(db *database.DB) *BunStore {
	return &BunStore{db: db}
}

func (store *BunStore) InsertUser(ctx context.Context, user *User) error {
	return ErrPersistenceNotImplemented
}

func (store *BunStore) UserByUsername(ctx context.Context, username string) (*User, error) {
	return nil, ErrPersistenceNotImplemented
}

func (store *BunStore) CreateGroup(ctx context.Context, name string) error {
	return ErrPersistenceNotImplemented
}

func (store *BunStore) CreatePermission(ctx context.Context, permission Permission) error {
	return ErrPersistenceNotImplemented
}

func (store *BunStore) GrantUserPermission(
	ctx context.Context,
	userID string,
	permission Permission,
) error {
	return ErrPersistenceNotImplemented
}

func (store *BunStore) AddUserToGroup(ctx context.Context, userID, groupName string) error {
	return ErrPersistenceNotImplemented
}

func (store *BunStore) GrantGroupPermission(
	ctx context.Context,
	groupName string,
	permission Permission,
) error {
	return ErrPersistenceNotImplemented
}

func (store *BunStore) RunInTx(
	ctx context.Context,
	fn func(context.Context, *BunStore) error,
) error {
	return ErrPersistenceNotImplemented
}
