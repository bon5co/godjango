//go:build integration

package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/migrations"
	"github.com/bon5co/godjango/project"
	"github.com/go-chi/chi/v5"
)

func TestPostgresBackedLoginPasswordChangeAndLogout(t *testing.T) {
	admin, db, schema := newWebIntegrationDatabase(t)
	ctx := context.Background()
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	password := "correct-password"
	user, err := manager.CreateUser(ctx, auth.CreateUserOptions{
		Username: "alice",
		Email:    "alice@EXAMPLE.COM",
		Password: &password,
	})
	if err != nil {
		t.Fatal(err)
	}

	sessionStore, err := NewSessionStore(store)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewSessions(SessionConfig{
		CookieName: "godjango_session",
		Lifetime:   time.Hour,
	}, sessionStore)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := NewCSRF(CSRFConfig{CookieName: "godjango_csrf"})
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("session-secret")
	handlers, err := NewAuthHandlers(AuthHandlerConfig{
		Backend:       manager,
		SessionSecret: secret,
		ResetTokens: auth.ResetTokenGenerator{
			Secret:  []byte("reset-secret"),
			Timeout: time.Hour,
		},
		SendReset: func(context.Context, ResetMessage) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(RequestID())
	router.Use(sessions.Middleware)
	router.Use(csrf.Middleware)
	router.Use(Authentication(manager, secret))
	handlers.Routes(router)
	router.Get("/", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-CSRF-Token", CSRFToken(request))
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	token := integrationCSRFToken(t, client, server.URL+"/accounts/login/")
	response := integrationPostForm(t, client, server.URL+"/accounts/login/", url.Values{
		"username":   {"alice"},
		"password":   {password},
		"csrf_token": {token},
	})
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d", response.StatusCode)
	}
	if rows := sessionRows(t, admin, schema); rows != 1 {
		t.Fatalf("session rows after login = %d, want 1", rows)
	}

	token = integrationCSRFToken(t, client, server.URL+"/accounts/password-change/")
	response = integrationPostForm(
		t,
		client,
		server.URL+"/accounts/password-change/",
		url.Values{
			"old_password":  {password},
			"new_password1": {"new-password"},
			"new_password2": {"new-password"},
			"csrf_token":    {token},
		},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("password change status = %d", response.StatusCode)
	}
	loaded, err := manager.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := loaded.CheckPassword(auth.NewPasswordHasher(), "new-password")
	if err != nil || !ok {
		t.Fatalf("new password check = %v, %v", ok, err)
	}
	oldOK, err := loaded.CheckPassword(auth.NewPasswordHasher(), password)
	if err != nil || oldOK {
		t.Fatalf("old password check = %v, %v", oldOK, err)
	}
	if rows := sessionRows(t, admin, schema); rows != 1 {
		t.Fatalf("session rows after password rotation = %d, want 1", rows)
	}

	token = integrationCSRFToken(t, client, server.URL+"/")
	response = integrationPostForm(t, client, server.URL+"/accounts/logout/", url.Values{
		"csrf_token": {token},
	})
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d", response.StatusCode)
	}
	if rows := sessionRows(t, admin, schema); rows != 0 {
		t.Fatalf("session rows after logout = %d, want 0", rows)
	}
}

func newWebIntegrationDatabase(t *testing.T) (*database.DB, *database.DB, string) {
	t.Helper()
	baseDSN := os.Getenv("GODJANGO_TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Fatal("GODJANGO_TEST_DATABASE_URL is required for integration tests")
	}
	ctx := context.Background()
	admin, err := database.Open(ctx, database.DefaultConfig(baseDSN))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	schemaName := fmt.Sprintf("godjango_auth_http_%d", time.Now().UnixNano())
	schema := `"` + schemaName + `"`
	if _, err := admin.Bun().ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Bun().ExecContext(
			context.Background(),
			"DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	db, err := database.Open(ctx, database.DefaultConfig(parsed.String()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	configured, err := project.New(webIntegrationSettings{}, auth.App)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := migrations.Collect(configured)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := migrations.NewRunner(db, catalog, migrations.DefaultRunnerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	return admin, db, schema
}

func integrationCSRFToken(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", target, response.StatusCode)
	}
	token := response.Header.Get("X-CSRF-Token")
	if token == "" {
		t.Fatalf("GET %s returned no CSRF token", target)
	}
	return token
}

func integrationPostForm(
	t *testing.T,
	client *http.Client,
	target string,
	values url.Values,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		target,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
