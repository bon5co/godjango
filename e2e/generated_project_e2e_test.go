//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/management"
	"github.com/chromedp/chromedp"
)

const (
	sessionSecret = "generated-project-e2e-session-secret"
	aliceUsername = "alice"
	alicePassword = "alice-first-password"
	adminUsername = "root"
	adminPassword = "root-password"
)

func TestGeneratedProjectProductionBrowserFlows(t *testing.T) {
	baseDSN := os.Getenv("GODJANGO_TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Fatal("GODJANGO_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(frameworkRoot, "test-results", "auth-e2e")
	if err := os.RemoveAll(results); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(results, 0o755); err != nil {
		t.Fatal(err)
	}

	admin, appDSN := isolatedDatabase(t, ctx, baseDSN)
	projectRoot, err := (management.Scaffolder{
		FrameworkVersion: "v0.0.0",
		FrameworkReplace: frameworkRoot,
	}).StartProject(ctx, t.TempDir(), "bookshelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := (management.Scaffolder{}).StartApp(projectRoot, "accounts"); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "accounts_routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(projectRoot, "apps", "accounts", "routes.go"),
		fixture,
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	environment := projectEnvironment(appDSN)
	run(t, ctx, projectRoot, environment, nil, "go", "mod", "tidy")
	run(t, ctx, projectRoot, environment, nil, "go", "test", "./...")
	run(t, ctx, projectRoot, environment, nil, "go", "vet", "./...")
	run(t, ctx, projectRoot, environment, nil, "go", "test", "-tags=integration", "./...")
	run(t, ctx, projectRoot, environment, nil, "go", "run", "./cmd/manage", "migrate")
	run(
		t,
		ctx,
		projectRoot,
		environment,
		strings.NewReader(adminPassword+"\n"),
		"go",
		"run",
		"./cmd/manage",
		"createsuperuser",
		"--username",
		adminUsername,
		"--email",
		"ROOT@EXAMPLE.COM",
		"--password-stdin",
	)
	binary := filepath.Join(projectRoot, "bookshelf-server")
	run(
		t,
		ctx,
		projectRoot,
		environment,
		nil,
		"go",
		"build",
		"-buildvcs=false",
		"-trimpath",
		"-o",
		binary,
		"./cmd/server",
	)

	baseURL, stopServer := startServer(t, projectRoot, binary, environment)
	defer stopServer()
	browser, stopBrowser := startBrowser(t)
	defer stopBrowser()

	visit(t, browser, baseURL+"/", "body")
	click(t, browser, `a[href="/register/"]`)
	wait(t, browser, `form[action="/register/"]`)
	screenshot(t, browser, results, "01-register-form.png")
	click(t, browser, `button[type="submit"]`)
	wait(t, browser, `[name="username"][aria-invalid="true"]`)
	assertJS(t, browser, `document.activeElement.name`, "username")
	screenshot(t, browser, results, "02-register-validation.png")

	fill(t, browser, `[name="username"]`, aliceUsername)
	fill(t, browser, `[name="email"]`, "ALICE@EXAMPLE.COM")
	fill(t, browser, `[name="password1"]`, alicePassword)
	fill(t, browser, `[name="password2"]`, alicePassword)
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "Signed in as alice")
	screenshot(t, browser, results, "03-registered.png")

	var persisted struct {
		ID          string `bun:"id"`
		Email       string `bun:"email"`
		IsStaff     bool   `bun:"is_staff"`
		IsSuperuser bool   `bun:"is_superuser"`
	}
	if err := admin.Bun().NewRaw(
		`SELECT id, email, is_staff, is_superuser
		 FROM auth_users WHERE username = ?`,
		aliceUsername,
	).Scan(ctx, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Email != "ALICE@example.com" || persisted.IsStaff || persisted.IsSuperuser {
		t.Fatalf("registered row = %+v", persisted)
	}
	requireSessionRows(t, ctx, admin, 1, "registration")
	sessionBeforeLogout := sessionKeys(t, ctx, admin)

	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "Anonymous")
	screenshot(t, browser, results, "04-logged-out.png")
	requireSessionRows(t, ctx, admin, 1, "logout")
	sessionAfterLogout := sessionKeys(t, ctx, admin)
	if sessionBeforeLogout[0] == sessionAfterLogout[0] {
		t.Fatal("logout did not replace the authenticated session")
	}

	click(t, browser, `a[href="/accounts/login/"]`)
	wait(t, browser, `form[action="/accounts/login/"]`)
	fill(t, browser, `[name="username"]`, aliceUsername)
	fill(t, browser, `[name="password"]`, "wrong-password")
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "credentials were not accepted")
	assertJS(t, browser, `document.activeElement.getAttribute("role")`, "alert")
	screenshot(t, browser, results, "05-login-rejected.png")

	fill(t, browser, `[name="password"]`, alicePassword)
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "Signed in as alice")
	screenshot(t, browser, results, "06-login-success.png")

	second, stopSecond := startBrowser(t)
	defer stopSecond()
	login(t, second, baseURL, aliceUsername, alicePassword)
	screenshot(t, second, results, "07-second-session.png")
	requireSessionRows(t, ctx, admin, 2, "second login")

	click(t, browser, `a[href="/accounts/password-change/"]`)
	wait(t, browser, `form[action="/accounts/password-change/"]`)
	fill(t, browser, `[name="old_password"]`, alicePassword)
	fill(t, browser, `[name="new_password1"]`, "alice-changed-password")
	fill(t, browser, `[name="new_password2"]`, "alice-changed-password")
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "Password changed.")
	screenshot(t, browser, results, "08-password-changed.png")
	assertPassword(
		t,
		ctx,
		admin,
		aliceUsername,
		"alice-changed-password",
		alicePassword,
	)

	visit(t, second, baseURL+"/publish/", "body")
	waitText(t, second, "authentication_required")
	screenshot(t, second, results, "09-stale-session-rejected.png")
	requireSessionRows(t, ctx, admin, 1, "stale session rejection")
	visit(t, browser, baseURL+"/", "body")
	waitText(t, browser, "Signed in as alice")

	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "Anonymous")
	visit(t, browser, baseURL+"/accounts/password-reset/", `form[action="/accounts/password-reset/"]`)
	fill(t, browser, `[name="email"]`, "missing@example.com")
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "If an account exists")
	screenshot(t, browser, results, "10-reset-missing.png")

	visit(t, browser, baseURL+"/accounts/password-reset/", `form[action="/accounts/password-reset/"]`)
	fill(t, browser, `[name="email"]`, "ALICE@example.com")
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "If an account exists")
	screenshot(t, browser, results, "11-reset-existing.png")

	store := auth.NewBunStore(admin)
	user, err := store.UserByUsername(ctx, aliceUsername)
	if err != nil {
		t.Fatal(err)
	}
	resetGenerator := auth.ResetTokenGenerator{
		Secret:  derive(sessionSecret, "password-reset"),
		Timeout: 24 * time.Hour,
	}
	token, err := resetGenerator.Make(user)
	if err != nil {
		t.Fatal(err)
	}
	resetURL := baseURL + "/accounts/password-reset/" +
		base64.RawURLEncoding.EncodeToString([]byte(user.ID)) + "/" +
		url.PathEscape(token) + "/"
	visit(t, browser, resetURL, `form[action]`)
	fill(t, browser, `[name="new_password1"]`, "alice-reset-password")
	fill(t, browser, `[name="new_password2"]`, "alice-reset-password")
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "Password reset complete.")
	screenshot(t, browser, results, "12-reset-complete.png")
	assertPassword(
		t,
		ctx,
		admin,
		aliceUsername,
		"alice-reset-password",
		"alice-changed-password",
	)
	visit(t, browser, resetURL, "body")
	waitText(t, browser, "invalid_reset")
	screenshot(t, browser, results, "13-reset-replay-rejected.png")

	login(t, browser, baseURL, aliceUsername, "alice-reset-password")
	visit(t, browser, baseURL+"/publish/", "body")
	waitText(t, browser, "permission_denied")
	screenshot(t, browser, results, "14-permission-denied.png")

	if err := store.CreatePermission(ctx, auth.Permission("library.publish_book")); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantUserPermission(
		ctx,
		persisted.ID,
		auth.Permission("library.publish_book"),
	); err != nil {
		t.Fatal(err)
	}
	requireCount(t, ctx, admin, 1, `SELECT count(*)
		FROM auth_user_permissions AS up
		JOIN auth_permissions AS p ON p.id = up.permission_id
		WHERE up.user_id = ? AND p.identity = ?`,
		persisted.ID,
		"library.publish_book",
	)
	visit(t, browser, baseURL+"/publish/", "body")
	waitText(t, browser, "Publishing allowed.")
	screenshot(t, browser, results, "15-direct-permission.png")

	if err := store.CreatePermission(ctx, auth.Permission("library.review_book")); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateGroup(ctx, "reviewers"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUserToGroup(ctx, persisted.ID, "reviewers"); err != nil {
		t.Fatal(err)
	}
	if err := store.GrantGroupPermission(
		ctx,
		"reviewers",
		auth.Permission("library.review_book"),
	); err != nil {
		t.Fatal(err)
	}
	requireCount(t, ctx, admin, 1, `SELECT count(*)
		FROM auth_user_groups AS ug
		JOIN auth_groups AS g ON g.id = ug.group_id
		JOIN auth_group_permissions AS gp ON gp.group_id = g.id
		JOIN auth_permissions AS p ON p.id = gp.permission_id
		WHERE ug.user_id = ? AND g.name = ? AND p.identity = ?`,
		persisted.ID,
		"reviewers",
		"library.review_book",
	)
	visit(t, browser, baseURL+"/review/", "body")
	waitText(t, browser, "Review allowed.")
	screenshot(t, browser, results, "16-group-permission.png")

	if _, err := admin.Bun().ExecContext(
		ctx,
		"UPDATE auth_users SET is_active = false WHERE id = ?",
		persisted.ID,
	); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err := admin.Bun().NewRaw(
		"SELECT is_active FROM auth_users WHERE id = ?",
		persisted.ID,
	).Scan(ctx, &active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("deactivation did not persist")
	}
	visit(t, browser, baseURL+"/", "body")
	waitText(t, browser, "Anonymous")
	screenshot(t, browser, results, "17-deactivated.png")

	login(t, browser, baseURL, adminUsername, adminPassword)
	visit(t, browser, baseURL+"/review/", "body")
	waitText(t, browser, "Review allowed.")
	screenshot(t, browser, results, "18-superuser.png")

	var superuser struct {
		IsStaff     bool `bun:"is_staff"`
		IsSuperuser bool `bun:"is_superuser"`
	}
	if err := admin.Bun().NewRaw(
		"SELECT is_staff, is_superuser FROM auth_users WHERE username = ?",
		adminUsername,
	).Scan(ctx, &superuser); err != nil {
		t.Fatal(err)
	}
	if !superuser.IsStaff || !superuser.IsSuperuser {
		t.Fatalf("superuser row = %+v", superuser)
	}
}

func isolatedDatabase(
	t *testing.T,
	ctx context.Context,
	baseDSN string,
) (*database.DB, string) {
	t.Helper()
	admin, err := database.Open(ctx, database.DefaultConfig(baseDSN))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	schema := fmt.Sprintf("godjango_e2e_%d", time.Now().UnixNano())
	quoted := `"` + schema + `"`
	if _, err := admin.Bun().ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Bun().ExecContext(
			context.Background(),
			"DROP SCHEMA "+quoted+" CASCADE",
		)
		if err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	app, err := database.Open(ctx, database.DefaultConfig(parsed.String()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	return app, parsed.String()
}

func projectEnvironment(dsn string) []string {
	return append(environmentWithout(
		os.Environ(),
		"DATABASE_URL",
		"SESSION_SECRET",
		"DEBUG",
	), "DATABASE_URL="+dsn, "SESSION_SECRET="+sessionSecret, "DEBUG=true")
}

func environmentWithout(environment []string, names ...string) []string {
	excluded := make(map[string]struct{}, len(names))
	for _, name := range names {
		excluded[name] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if _, remove := excluded[name]; !remove {
			result = append(result, item)
		}
	}
	return result
}

func run(
	t *testing.T,
	ctx context.Context,
	directory string,
	environment []string,
	stdin io.Reader,
	name string,
	args ...string,
) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.Env = environment
	command.Stdin = stdin
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func startServer(
	t *testing.T,
	directory string,
	binary string,
	environment []string,
) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, address)
	command.Dir = directory
	command.Env = environment
	var logs bytes.Buffer
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if command.Process != nil {
			_ = command.Process.Signal(os.Interrupt)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}
	baseURL := "http://" + address
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, requestErr := client.Get(baseURL + "/healthz")
		if requestErr == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return baseURL, stop
			}
		}
		if command.ProcessState != nil && command.ProcessState.Exited() {
			t.Fatalf("server exited early:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	t.Fatalf("server did not become ready:\n%s", logs.String())
	return "", func() {}
}

func startBrowser(t *testing.T) (context.Context, func()) {
	t.Helper()
	chromium := os.Getenv("GODJANGO_CHROME_PATH")
	if chromium == "" {
		for _, candidate := range []string{"chromium", "chrome", "google-chrome"} {
			if resolved, err := exec.LookPath(candidate); err == nil {
				chromium = resolved
				break
			}
		}
	}
	if chromium == "" {
		t.Fatal("headed Chromium or Chrome is required")
	}
	profile, err := os.MkdirTemp("", "godjango-e2e-chromium-*")
	if err != nil {
		t.Fatal(err)
	}
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.UserDataDir(profile),
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		options = append(options, chromedp.Flag("ozone-platform", "wayland"))
	}
	if os.Getenv("GODJANGO_CHROME_NO_SANDBOX") == "true" {
		options = append(options, chromedp.Flag("no-sandbox", true))
	}
	allocator, stopAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	browser, stopBrowser := chromedp.NewContext(allocator)
	if err := chromedp.Run(browser); err != nil {
		stopBrowser()
		stopAllocator()
		_ = os.RemoveAll(profile)
		t.Fatal(err)
	}
	return browser, func() {
		_ = chromedp.Cancel(browser)
		stopBrowser()
		stopAllocator()
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := os.RemoveAll(profile)
			if err == nil || time.Now().After(deadline) {
				if err != nil {
					t.Errorf("remove Chromium profile: %v", err)
				}
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

func visit(t *testing.T, browser context.Context, target string, selector string) {
	t.Helper()
	if err := chromedp.Run(
		browser,
		chromedp.Navigate(target),
		chromedp.WaitVisible(selector, chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
}

func click(t *testing.T, browser context.Context, selector string) {
	t.Helper()
	if err := chromedp.Run(
		browser,
		chromedp.Click(selector, chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
}

func fill(t *testing.T, browser context.Context, selector string, value string) {
	t.Helper()
	if err := chromedp.Run(
		browser,
		chromedp.SetValue(selector, value, chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
}

func wait(t *testing.T, browser context.Context, selector string) {
	t.Helper()
	if err := chromedp.Run(
		browser,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
}

func waitText(t *testing.T, browser context.Context, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var body string
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = chromedp.Run(
			browser,
			chromedp.Text("body", &body, chromedp.ByQuery),
		)
		if lastErr == nil && strings.Contains(body, text) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("page did not contain %q: body=%q error=%v", text, body, lastErr)
}

func assertJS(t *testing.T, browser context.Context, expression string, want string) {
	t.Helper()
	var got string
	if err := chromedp.Run(browser, chromedp.Evaluate(expression, &got)); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", expression, got, want)
	}
}

func screenshot(
	t *testing.T,
	browser context.Context,
	directory string,
	name string,
) {
	t.Helper()
	var image []byte
	if err := chromedp.Run(browser, chromedp.FullScreenshot(&image, 90)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), image, 0o644); err != nil {
		t.Fatal(err)
	}
}

func login(
	t *testing.T,
	browser context.Context,
	baseURL string,
	username string,
	password string,
) {
	t.Helper()
	visit(t, browser, baseURL+"/accounts/login/", `form[action="/accounts/login/"]`)
	fill(t, browser, `[name="username"]`, username)
	fill(t, browser, `[name="password"]`, password)
	click(t, browser, `button[type="submit"]`)
	waitText(t, browser, "Signed in as "+username)
}

func requireSessionRows(
	t *testing.T,
	ctx context.Context,
	db *database.DB,
	want int,
	phase string,
) {
	t.Helper()
	var count int
	if err := db.Bun().NewRaw("SELECT count(*) FROM auth_sessions").Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s session rows = %d, want %d", phase, count, want)
	}
}

func sessionKeys(t *testing.T, ctx context.Context, db *database.DB) []string {
	t.Helper()
	var keys []string
	if err := db.Bun().NewRaw(
		"SELECT session_key FROM auth_sessions ORDER BY session_key",
	).Scan(ctx, &keys); err != nil {
		t.Fatal(err)
	}
	return keys
}

func assertPassword(
	t *testing.T,
	ctx context.Context,
	db *database.DB,
	username string,
	current string,
	old string,
) {
	t.Helper()
	var encoded string
	if err := db.Bun().NewRaw(
		"SELECT password_hash FROM auth_users WHERE username = ?",
		username,
	).Scan(ctx, &encoded); err != nil {
		t.Fatal(err)
	}
	hasher := auth.NewPasswordHasher()
	result, err := hasher.Check(&current, encoded)
	if err != nil || !result.OK {
		t.Fatalf("current password check = %+v, %v", result, err)
	}
	result, err = hasher.Check(&old, encoded)
	if err != nil || result.OK {
		t.Fatalf("old password check = %+v, %v", result, err)
	}
}

func requireCount(
	t *testing.T,
	ctx context.Context,
	db *database.DB,
	want int,
	query string,
	args ...any,
) {
	t.Helper()
	var count int
	if err := db.Bun().NewRaw(query, args...).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("row count = %d, want %d", count, want)
	}
}

func derive(secret string, purpose string) []byte {
	sum := sha256.Sum256([]byte(purpose + "\x00" + secret))
	return sum[:]
}
