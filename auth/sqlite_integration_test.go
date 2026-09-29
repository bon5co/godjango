//go:build sqlite_integration

package auth_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/migrations"
	"github.com/bon5co/godjango/project"
)

type sqliteSettings struct{}

func (sqliteSettings) Validate() error { return nil }

func TestSQLiteAuthAndSessionsPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dsn := "sqlite:" + filepath.Join(t.TempDir(), "auth.sqlite")
	db, err := database.Open(ctx, database.DefaultSQLiteConfig(dsn))
	if err != nil {
		t.Fatal(err)
	}
	configured, err := project.New(sqliteSettings{}, auth.App)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := migrations.CollectForDialect(configured, db.Dialect())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := migrations.NewRunner(db, catalog, migrations.DefaultRunnerConfig())
	if err != nil {
		t.Fatal(err)
	}
	applied, err := runner.Apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied %v", applied)
	}
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	password := "secret"
	user, err := manager.CreateUser(ctx, auth.CreateUserOptions{Username: "test", Password: &password})
	if err != nil {
		t.Fatal(err)
	}
	if user.ID == "" {
		t.Fatal("empty user ID")
	}
	if err := store.RunInTx(ctx, func(ctx context.Context, txStore *auth.BunStore) error {
		_, err := auth.NewManager(txStore, auth.NewPasswordHasher()).CreateUser(ctx, auth.CreateUserOptions{Username: "in_tx", Password: &password})
		return err
	}); err != nil {
		t.Fatalf("SQLite auth transaction: %v", err)
	}
	if err := store.CreateGroup(ctx, "admins"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePermission(ctx, auth.Permission("app.read")); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantGroupPermission(ctx, "admins", auth.Permission("app.read")); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUserToGroup(ctx, user.ID, "admins"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(ctx, auth.StoredSession{Key: "token", Data: []byte("payload"), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(ctx, database.DefaultSQLiteConfig(dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := auth.NewBunStore(reopened).UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != user.ID || len(loaded.Groups) != 1 || len(loaded.Groups[0].Permissions) != 1 {
		t.Fatalf("loaded user %+v", loaded)
	}
	session, err := auth.NewBunStore(reopened).StoredSession(ctx, "token")
	if err != nil || string(session.Data) != "payload" {
		t.Fatalf("session %+v, %v", session, err)
	}
	var count int
	if err := reopened.Bun().NewRaw("SELECT count(*) FROM auth_users WHERE id = ?", user.ID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted rows = %d", count)
	}
	var storageType string
	if err := reopened.Bun().NewRaw("SELECT typeof(date_joined) FROM auth_users WHERE id = ?", user.ID).Scan(ctx, &storageType); err != nil {
		t.Fatal(err)
	}
	if storageType != "text" {
		t.Fatalf("date_joined storage = %q, want text", storageType)
	}
}
