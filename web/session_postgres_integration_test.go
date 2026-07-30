//go:build integration

package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/migrations"
	"github.com/bon5co/godjango/project"
)

type webIntegrationSettings struct{}

func (webIntegrationSettings) Validate() error { return nil }

func TestPostgresSessionRotationAndInvalidationAtRowLevel(t *testing.T) {
	baseDSN := os.Getenv("GODJANGO_TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Fatal("GODJANGO_TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	admin, err := database.Open(ctx, database.DefaultConfig(baseDSN))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	schema := fmt.Sprintf("godjango_web_%d", time.Now().UnixNano())
	quotedSchema := `"` + schema + `"`
	if _, err := admin.Bun().ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer admin.Bun().ExecContext(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := database.Open(ctx, database.DefaultConfig(parsed.String()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	configured, err := project.New(webIntegrationSettings{}, auth.App)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := migrations.Collect(configured)
	if err != nil {
		t.Fatal(err)
	}
	runnerConfig := migrations.DefaultRunnerConfig()
	runner, err := migrations.NewRunner(db, catalog, runnerConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}

	store, err := NewSessionStore(auth.NewBunStore(db))
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewSessions(SessionConfig{
		CookieName: "godjango_session",
		Lifetime:   time.Hour,
		SameSite:   http.SameSiteLaxMode,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	handler := sessions.Middleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		session := SessionFromRequest(request)
		switch request.URL.Path {
		case "/set":
			session.Put("cart", "book")
		case "/login":
			user := &auth.User{ID: "user-1", PasswordHash: "encoded"}
			if err := auth.Login(session, user, []byte("secret")); err != nil {
				t.Fatal(err)
			}
		case "/logout":
			if err := auth.Logout(session); err != nil {
				t.Fatal(err)
			}
		}
		_, _ = io.WriteString(response, session.ID())
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	setResponse, err := client.Get(server.URL + "/set")
	if err != nil {
		t.Fatal(err)
	}
	setResponse.Body.Close()
	oldID := getBody(t, client, server.URL+"/set")
	if rows := sessionRows(t, admin, quotedSchema); rows != 1 {
		t.Fatalf("session rows after set = %d, want 1", rows)
	}
	newID := getBody(t, client, server.URL+"/login")
	if oldID == "" || newID == "" || oldID == newID {
		t.Fatalf("session rotation old=%q new=%q", oldID, newID)
	}
	if rows := sessionRows(t, admin, quotedSchema); rows != 1 {
		t.Fatalf("session rows after rotation = %d, want 1", rows)
	}
	logoutResponse, err := client.Get(server.URL + "/logout")
	if err != nil {
		t.Fatal(err)
	}
	logoutResponse.Body.Close()
	if rows := sessionRows(t, admin, quotedSchema); rows != 0 {
		t.Fatalf("session rows after logout = %d, want 0", rows)
	}
}

func sessionRows(t *testing.T, db *database.DB, schema string) int {
	t.Helper()
	var count int
	if err := db.Bun().NewRaw(
		"SELECT count(*) FROM "+schema+".auth_sessions",
	).Scan(context.Background(), &count); err != nil {
		t.Fatal(err)
	}
	return count
}
