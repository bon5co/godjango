//go:build integration

package auth_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/migrations"
	"github.com/bon5co/godjango/project"
)

type authFixtureSettings struct{}

func (authFixtureSettings) Validate() error { return nil }

func authTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("GODJANGO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("GODJANGO_TEST_DATABASE_URL is required for integration tests")
	}
	return dsn
}

func authDatabase(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(
		context.Background(),
		database.DefaultConfig(authTestDSN(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	configured, err := project.New(authFixtureSettings{}, auth.App)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := migrations.Collect(configured)
	if err != nil {
		t.Fatal(err)
	}
	config := migrations.DefaultRunnerConfig()
	config.Table = "godjango_auth_test_migrations"
	config.LocksTable = "godjango_auth_test_migration_locks"
	runner, err := migrations.NewRunner(db, catalog, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func uniqueAuthValue(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("%s_%d_%s", t.Name(), time.Now().UnixNano(), suffix)
}

func TestAuthMigrationsCreateEveryPersistenceTable(t *testing.T) {
	db := authDatabase(t)
	ctx := context.Background()
	for _, table := range []string{
		"auth_users",
		"auth_groups",
		"auth_permissions",
		"auth_user_groups",
		"auth_user_permissions",
		"auth_group_permissions",
		"auth_sessions",
	} {
		var relation *string
		if err := db.Bun().
			NewRaw("SELECT to_regclass(?)", table).
			Scan(ctx, &relation); err != nil {
			t.Fatal(err)
		}
		if relation == nil {
			t.Errorf("migration did not create %s", table)
		}
	}
}

func TestBunStoreCreatesAndLoadsUserAtRowLevel(t *testing.T) {
	db := authDatabase(t)
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	username := uniqueAuthValue(t, "user")
	password := "secret"

	created, err := manager.CreateUser(context.Background(), auth.CreateUserOptions{
		Username: username,
		Email:    "CaseSensitive@EXAMPLE.COM",
		Password: &password,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateUser() ID is empty")
	}

	var count int
	if err := db.Bun().NewRaw(
		"SELECT count(*) FROM auth_users WHERE id = ? AND username = ? AND email = ?",
		created.ID,
		username,
		"CaseSensitive@example.com",
	).Scan(context.Background(), &count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted matching rows = %d, want 1", count)
	}

	loaded, err := store.UserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("UserByUsername() error = %v", err)
	}
	if loaded.ID != created.ID || loaded.PasswordHash != created.PasswordHash {
		t.Fatalf("loaded user = %+v, created = %+v", loaded, created)
	}
}

func TestConcurrentDuplicateUsernameHasOneWinner(t *testing.T) {
	db := authDatabase(t)
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	username := uniqueAuthValue(t, "duplicate")
	password := "secret"

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := manager.CreateUser(context.Background(), auth.CreateUserOptions{
				Username: username,
				Password: &password,
			})
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("duplicate race: successes=%d failures=%d", successes, failures)
	}
	var count int
	if err := db.Bun().
		NewRaw("SELECT count(*) FROM auth_users WHERE username = ?", username).
		Scan(context.Background(), &count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted duplicate rows = %d, want 1", count)
	}
}

func TestDirectAndGroupPermissionsLoadThroughDomainModel(t *testing.T) {
	db := authDatabase(t)
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	ctx := context.Background()
	username := uniqueAuthValue(t, "permissions")
	group := uniqueAuthValue(t, "editors")
	user, err := manager.CreateUser(ctx, auth.CreateUserOptions{Username: username})
	if err != nil {
		t.Fatal(err)
	}
	for _, permission := range []auth.Permission{"books.change_book", "books.view_book"} {
		if err := store.CreatePermission(ctx, permission); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantUserPermission(ctx, user.ID, "books.change_book"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUserToGroup(ctx, user.ID, group); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantGroupPermission(ctx, group, "books.view_book"); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.UserByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	backend := auth.ModelBackend{}
	for _, permission := range []auth.Permission{"books.change_book", "books.view_book"} {
		if !backend.HasPermission(loaded, permission) {
			t.Errorf("loaded user missing %s", permission)
		}
	}
}

func TestPermissionTransactionRollsBackEveryWrite(t *testing.T) {
	db := authDatabase(t)
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	ctx := context.Background()
	username := uniqueAuthValue(t, "rollback")
	user, err := manager.CreateUser(ctx, auth.CreateUserOptions{Username: username})
	if err != nil {
		t.Fatal(err)
	}
	permission := auth.Permission("books.delete_book")
	rollbackErr := errors.New("rollback")

	err = store.RunInTx(ctx, func(ctx context.Context, txStore *auth.BunStore) error {
		if err := txStore.CreatePermission(ctx, permission); err != nil {
			return err
		}
		if err := txStore.GrantUserPermission(ctx, user.ID, permission); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("RunInTx() error = %v", err)
	}
	loaded, err := store.UserByUsername(ctx, username)
	if err != nil {
		t.Fatal(err)
	}
	if (auth.ModelBackend{}).HasPermission(loaded, permission) {
		t.Fatal("rolled-back permission remained visible")
	}
}

func TestConcurrentPermissionGrantIsIdempotent(t *testing.T) {
	db := authDatabase(t)
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	ctx := context.Background()
	username := uniqueAuthValue(t, "concurrent_permission")
	user, err := manager.CreateUser(ctx, auth.CreateUserOptions{Username: username})
	if err != nil {
		t.Fatal(err)
	}
	permission := auth.Permission("books.publish_book")
	if err := store.CreatePermission(ctx, permission); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- store.GrantUserPermission(ctx, user.ID, permission)
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("GrantUserPermission() error = %v", err)
		}
	}

	var count int
	if err := db.Bun().NewRaw(
		`SELECT count(*) FROM auth_user_permissions AS up
		 JOIN auth_permissions AS p ON p.id = up.permission_id
		 WHERE up.user_id = ? AND p.identity = ?`,
		user.ID,
		permission,
	).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted permission rows = %d, want 1", count)
	}
}

func TestSessionPersistenceRoundTripUpdateDeleteAndExpiry(t *testing.T) {
	db := authDatabase(t)
	store := auth.NewBunStore(db)
	ctx := context.Background()
	now := time.Now().UTC()
	key := uniqueAuthValue(t, "session")

	session := auth.StoredSession{
		Key:       key,
		Data:      []byte("first"),
		ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession() error = %v", err)
	}
	loaded, err := store.StoredSession(ctx, key)
	if err != nil {
		t.Fatalf("StoredSession() error = %v", err)
	}
	if string(loaded.Data) != "first" || !loaded.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("StoredSession() = %+v", loaded)
	}

	session.Data = []byte("updated")
	if err := store.SaveSession(ctx, session); err != nil {
		t.Fatalf("updating SaveSession() error = %v", err)
	}
	loaded, err = store.StoredSession(ctx, key)
	if err != nil || string(loaded.Data) != "updated" {
		t.Fatalf("updated StoredSession() = %+v, %v", loaded, err)
	}
	if err := store.DeleteSession(ctx, key); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if _, err := store.StoredSession(ctx, key); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("StoredSession(deleted) error = %v", err)
	}

	expiredKey := uniqueAuthValue(t, "expired")
	if err := store.SaveSession(ctx, auth.StoredSession{
		Key:       expiredKey,
		Data:      []byte("expired"),
		ExpiresAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteExpiredSessions(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions() error = %v", err)
	}
	if deleted < 1 {
		t.Fatalf("DeleteExpiredSessions() = %d, want at least 1", deleted)
	}
	if _, err := store.StoredSession(ctx, expiredKey); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("expired session error = %v", err)
	}
}
