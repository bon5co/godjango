package web

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2/memstore"
	"github.com/bon5co/godjango/auth"
	"github.com/go-chi/chi/v5"
)

type fakeAuthBackend struct {
	users       map[string]*auth.User
	passwords   map[string]string
	resetEmails []ResetMessage
}

func (backend *fakeAuthBackend) Authenticate(
	_ context.Context,
	username string,
	password string,
) (*auth.User, error) {
	user := backend.users[username]
	if user == nil || !user.IsActive || backend.passwords[username] != password {
		return nil, nil
	}
	return user, nil
}

func (backend *fakeAuthBackend) UserByID(_ context.Context, id string) (*auth.User, error) {
	for _, user := range backend.users {
		if user.ID == id {
			return user, nil
		}
	}
	return nil, auth.ErrUserNotFound
}

func (backend *fakeAuthBackend) UsersByEmail(_ context.Context, email string) ([]*auth.User, error) {
	var users []*auth.User
	for _, user := range backend.users {
		if user.Email == email {
			users = append(users, user)
		}
	}
	return users, nil
}

func (backend *fakeAuthBackend) SetPassword(
	_ context.Context,
	user *auth.User,
	password string,
) error {
	backend.passwords[user.Username] = password
	user.PasswordHash = "encoded-" + password
	return nil
}

func TestLoginRejectsUnsafeRedirectAndRotatesSession(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	// Browsing anonymously must not establish a session. The CSRF secret
	// lives in its own cookie, so nothing about rendering a login form
	// requires server-side storage.
	fixture.csrfToken(t, "/")
	if cookie := optionalCookie(fixture.client.Jar.Cookies(mustURL(t, fixture.server.URL)), "godjango_session"); cookie != nil {
		t.Fatal("an anonymous request established a session")
	}
	csrfToken := fixture.csrfToken(t, "/accounts/login/")

	response := fixture.postForm(t, "/accounts/login/", url.Values{
		"username":   {"alice"},
		"password":   {"correct"},
		"next":       {"https://evil.example/steal"},
		"csrf_token": {csrfToken},
	})
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/" {
		t.Fatalf("location = %q, want /", location)
	}
	loginCookie := namedCookie(t, response.Cookies(), "godjango_session")
	if loginCookie.Value == "" {
		t.Fatal("login did not establish a session")
	}
	response.Body.Close()

	protected := fixture.get(t, "/protected")
	body, err := io.ReadAll(protected.Body)
	protected.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if protected.StatusCode != http.StatusOK || string(body) != "alice" {
		t.Fatalf("protected status=%d body=%q", protected.StatusCode, body)
	}
}

// An HTTPS site must keep refusing a plaintext ?next=. Reading request.TLS for
// that decision dropped the rule on every deployment behind a TLS-terminating
// proxy and kept it only where nobody is attacked -- development.
func TestLoginKeepsTheHTTPSRedirectRuleBehindATerminatingProxy(t *testing.T) {
	fixture := newAuthHTTPFixture(t, TrustedProxy(TrustedProxyConfig{TrustAnyPeer: true}))
	fixture.headers.Set("X-Forwarded-Proto", "https")
	host := mustURL(t, fixture.server.URL).Host
	fixture.headers.Set("Origin", "https://"+host)

	for _, test := range []struct {
		name string
		next string
		want string
	}{
		{
			name: "plaintext target on the same host is a downgrade",
			next: "http://" + host + "/dashboard",
			want: "/",
		},
		{
			name: "the same target over https is allowed",
			next: "https://" + host + "/dashboard",
			want: "https://" + host + "/dashboard",
		},
		{name: "a relative target is unaffected", next: "/dashboard", want: "/dashboard"},
	} {
		t.Run(test.name, func(t *testing.T) {
			token := fixture.csrfToken(t, "/accounts/login/")
			response := fixture.postForm(t, "/accounts/login/", url.Values{
				"username":   {"alice"},
				"password":   {"correct"},
				"next":       {test.next},
				"csrf_token": {token},
			})
			response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", response.StatusCode)
			}
			if location := response.Header.Get("Location"); location != test.want {
				t.Fatalf("location = %q, want %q", location, test.want)
			}
		})
	}
}

func TestSuccessfulHTMXLoginUsesBrowserRedirectHeader(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	token := fixture.csrfToken(t, "/accounts/login/")
	values := url.Values{
		"username":   {"alice"},
		"password":   {"correct"},
		"csrf_token": {token},
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+"/accounts/login/",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.StatusCode)
	}
	if got := response.Header.Get("HX-Redirect"); got != "/" {
		t.Fatalf("HX-Redirect = %q, want /", got)
	}
}

func TestLoginFailureIsGenericAndSafelyRedisplaysUsername(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	token := fixture.csrfToken(t, "/accounts/login/")
	response := fixture.postForm(t, "/accounts/login/", url.Values{
		"username":   {`<script>alert("x")</script>`},
		"password":   {"wrong"},
		"csrf_token": {token},
	})
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%q", response.StatusCode, body)
	}
	text := string(body)
	if !strings.Contains(text, "credentials were not accepted") {
		t.Fatalf("body = %q", text)
	}
	if strings.Contains(text, "<script>") || !strings.Contains(text, "&lt;script&gt;") {
		t.Fatalf("username was not safely redisplayed: %q", text)
	}
}

func TestLoginValidationReturnsHTMXFormFragment(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	token := fixture.csrfToken(t, "/accounts/login/")
	values := url.Values{
		"username":   {"alice"},
		"password":   {"wrong"},
		"csrf_token": {token},
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+"/accounts/login/",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%q", response.StatusCode, text)
	}
	if strings.Contains(text, "<!doctype html>") ||
		!strings.HasPrefix(text, `<form`) ||
		!strings.Contains(text, "autofocus") {
		t.Fatalf("response is not a focused form fragment: %q", text)
	}
	if got := response.Header.Get("Vary"); !strings.Contains(got, "HX-Request") {
		t.Fatalf("Vary = %q", got)
	}
}

func TestLogoutRequiresPostAndCSRFThenInvalidatesSession(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	fixture.login(t)

	getResponse := fixture.get(t, "/accounts/logout/")
	getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET logout status = %d, want 405", getResponse.StatusCode)
	}
	noCSRF := fixture.postForm(t, "/accounts/logout/", url.Values{})
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF = %d, want 403", noCSRF.StatusCode)
	}
	token := fixture.csrfToken(t, "/")
	response := fixture.postForm(t, "/accounts/logout/", url.Values{"csrf_token": {token}})
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303", response.StatusCode)
	}
	protected := fixture.get(t, "/protected")
	protected.Body.Close()
	if protected.StatusCode != http.StatusUnauthorized {
		t.Fatalf("protected after logout = %d, want 401", protected.StatusCode)
	}
}

func TestInactiveUserIsRemovedFromAuthenticatedSession(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	fixture.login(t)
	fixture.backend.users["alice"].IsActive = false

	response := fixture.get(t, "/protected")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("inactive protected status = %d, want 401", response.StatusCode)
	}

	fixture.backend.users["alice"].IsActive = true
	response = fixture.get(t, "/protected")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("flushed session status = %d, want 401", response.StatusCode)
	}
}

func TestPasswordChangeChecksOldPasswordRotatesSessionAndUpdatesHash(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	fixture.login(t)
	before := fixture.sessionCookie(t)
	token := fixture.csrfToken(t, "/accounts/password-change/")

	response := fixture.postForm(t, "/accounts/password-change/", url.Values{
		"old_password":  {"correct"},
		"new_password1": {"new-password"},
		"new_password2": {"new-password"},
		"csrf_token":    {token},
	})
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	after := namedCookie(t, response.Cookies(), "godjango_session")
	if after.Value == before.Value {
		t.Fatal("password change did not rotate session")
	}
	if got := fixture.backend.passwords["alice"]; got != "new-password" {
		t.Fatalf("stored password = %q", got)
	}
	protected := fixture.get(t, "/protected")
	protected.Body.Close()
	if protected.StatusCode != http.StatusOK {
		t.Fatalf("current session was invalidated: %d", protected.StatusCode)
	}
}

func TestPasswordResetAvoidsEnumerationAndTokenIsSingleUse(t *testing.T) {
	fixture := newAuthHTTPFixture(t)
	for _, email := range []string{"alice@example.com", "missing@example.com"} {
		token := fixture.csrfToken(t, "/accounts/password-reset/")
		response := fixture.postForm(t, "/accounts/password-reset/", url.Values{
			"email":      {email},
			"csrf_token": {token},
		})
		response.Body.Close()
		if response.StatusCode != http.StatusSeeOther ||
			response.Header.Get("Location") != "/accounts/password-reset/done/" {
			t.Fatalf("email=%s status=%d location=%q", email, response.StatusCode, response.Header.Get("Location"))
		}
	}
	if len(fixture.backend.resetEmails) != 1 {
		t.Fatalf("reset messages = %d, want 1", len(fixture.backend.resetEmails))
	}
	message := fixture.backend.resetEmails[0]
	confirmPath := "/accounts/password-reset/" +
		base64.RawURLEncoding.EncodeToString([]byte(message.User.ID)) +
		"/" + url.PathEscape(message.Token) + "/"
	token := fixture.csrfToken(t, confirmPath)
	response := fixture.postForm(t, confirmPath, url.Values{
		"new_password1": {"reset-password"},
		"new_password2": {"reset-password"},
		"csrf_token":    {token},
	})
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset confirm status = %d", response.StatusCode)
	}
	if fixture.backend.passwords["alice"] != "reset-password" {
		t.Fatal("reset password was not stored")
	}
	token = fixture.csrfToken(t, "/")
	reused := fixture.postForm(t, confirmPath, url.Values{
		"new_password1": {"another-password"},
		"new_password2": {"another-password"},
		"csrf_token":    {token},
	})
	reused.Body.Close()
	if reused.StatusCode != http.StatusBadRequest {
		t.Fatalf("reused reset status = %d, want 400", reused.StatusCode)
	}
}

type authHTTPFixture struct {
	t       *testing.T
	server  *httptest.Server
	client  *http.Client
	backend *fakeAuthBackend
	// headers ride on every fixture request, so a test can reproduce what a
	// reverse proxy adds in front of the application.
	headers http.Header
}

// newAuthHTTPFixture serves over plain HTTP, which is also what a deployment
// behind a TLS-terminating proxy looks like from inside the process. Extra
// middleware runs outermost, before request IDs and sessions.
func newAuthHTTPFixture(t *testing.T, outer ...Middleware) *authHTTPFixture {
	t.Helper()
	user := &auth.User{
		ID:           "user-1",
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: "encoded-correct",
		IsActive:     true,
	}
	backend := &fakeAuthBackend{
		users:     map[string]*auth.User{"alice": user},
		passwords: map[string]string{"alice": "correct"},
	}
	sessions, err := NewSessions(SessionConfig{
		CookieName: "godjango_session",
		Lifetime:   time.Hour,
		SameSite:   http.SameSiteLaxMode,
	}, memstore.New())
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := NewCSRF(CSRFConfig{
		CookieName: "godjango_csrf",
		SameSite:   http.SameSiteLaxMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.ResetTokenGenerator{
		Secret:  []byte("reset-secret"),
		Timeout: time.Hour,
	}
	handlers, err := NewAuthHandlers(AuthHandlerConfig{
		Backend:       backend,
		SessionSecret: []byte("session-secret"),
		ResetTokens:   tokens,
		SendReset: func(_ context.Context, message ResetMessage) error {
			backend.resetEmails = append(backend.resetEmails, message)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	for _, middleware := range outer {
		router.Use(middleware)
	}
	router.Use(RequestID())
	router.Use(sessions.Middleware)
	router.Use(csrf.Middleware)
	router.Use(Authentication(backend, []byte("session-secret")))
	handlers.Routes(router)
	router.With(RequireAuthentication()).Get("/protected", func(response http.ResponseWriter, request *http.Request) {
		user, _ := CurrentUser(request)
		_, _ = io.WriteString(response, user.Username)
	})
	router.Get("/", func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, CSRFToken(request))
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
	return &authHTTPFixture{
		t:       t,
		server:  server,
		client:  client,
		backend: backend,
		headers: http.Header{},
	}
}

func (fixture *authHTTPFixture) get(t *testing.T, path string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, fixture.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture.applyHeaders(request)
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (fixture *authHTTPFixture) applyHeaders(request *http.Request) {
	for name, values := range fixture.headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
}

func (fixture *authHTTPFixture) csrfToken(t *testing.T, path string) string {
	t.Helper()
	response := fixture.get(t, path)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("CSRF page %s status=%d body=%q", path, response.StatusCode, body)
	}
	token := response.Header.Get("X-CSRF-Token")
	if token == "" {
		token = string(body)
	}
	if token == "" {
		t.Fatalf("CSRF token missing from %s", path)
	}
	return token
}

func (fixture *authHTTPFixture) postForm(
	t *testing.T,
	path string,
	values url.Values,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+path,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	fixture.applyHeaders(request)
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func optionalCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func (fixture *authHTTPFixture) sessionCookie(t *testing.T) *http.Cookie {
	t.Helper()
	cookies := fixture.client.Jar.Cookies(mustURL(t, fixture.server.URL))
	return namedCookie(t, cookies, "godjango_session")
}

func (fixture *authHTTPFixture) login(t *testing.T) {
	t.Helper()
	token := fixture.csrfToken(t, "/accounts/login/")
	response := fixture.postForm(t, "/accounts/login/", url.Values{
		"username":   {"alice"},
		"password":   {"correct"},
		"csrf_token": {token},
	})
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d", response.StatusCode)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
