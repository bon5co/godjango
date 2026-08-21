package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2/memstore"
	"github.com/bon5co/godjango/auth"
)

// csrfStorageFixture serves one handler behind the session and CSRF
// middleware and counts how many distinct sessions the store was asked to
// hold, which is the number this change exists to keep at zero for anonymous
// traffic.
type csrfStorageFixture struct {
	server *httptest.Server
	client *http.Client
	store  *memstore.MemStore
}

func newCSRFStorageFixture(t *testing.T, useSessions bool) *csrfStorageFixture {
	t.Helper()
	store := memstore.New()
	sessions, err := NewSessions(SessionConfig{
		CookieName: "godjango_session",
		Lifetime:   time.Hour,
		SameSite:   http.SameSiteLaxMode,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := NewCSRF(CSRFConfig{
		CookieName:  "godjango_csrf",
		SameSite:    http.SameSiteLaxMode,
		UseSessions: useSessions,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := sessions.Middleware(csrf.Middleware(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/form":
				_, _ = io.WriteString(response, CSRFToken(request))
			case "/submit":
				response.WriteHeader(http.StatusNoContent)
			case "/login":
				user := &auth.User{ID: "user-1", PasswordHash: "encoded"}
				if err := auth.Login(SessionFromRequest(request), user, []byte("secret")); err != nil {
					t.Error(err)
				}
				_, _ = io.WriteString(response, CSRFToken(request))
			}
		},
	)))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	return &csrfStorageFixture{server: server, client: client, store: store}
}

func (fixture *csrfStorageFixture) storedSessions(t *testing.T) int {
	t.Helper()
	all, err := fixture.store.All()
	if err != nil {
		t.Fatal(err)
	}
	return len(all)
}

func (fixture *csrfStorageFixture) formToken(t *testing.T) string {
	t.Helper()
	response, err := fixture.client.Get(fixture.server.URL + "/form")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The defect this replaces: rendering any page asked the session for a CSRF
// secret, which created a session for a caller that had none, which wrote a
// row. Sustained anonymous traffic grew the session table without bound.
func TestAnonymousBrowsingStoresNoSessionByDefault(t *testing.T) {
	fixture := newCSRFStorageFixture(t, false)

	for range 25 {
		if token := fixture.formToken(t); token == "" {
			t.Fatal("no CSRF token issued to an anonymous caller")
		}
	}

	if stored := fixture.storedSessions(t); stored != 0 {
		t.Fatalf("anonymous browsing stored %d sessions, want 0", stored)
	}
	cookies := fixture.client.Jar.Cookies(mustURL(t, fixture.server.URL))
	if optionalCookie(cookies, "godjango_session") != nil {
		t.Fatal("anonymous browsing issued a session cookie")
	}
	if optionalCookie(cookies, "godjango_csrf") == nil {
		t.Fatal("no CSRF cookie issued, so no form could be submitted")
	}
}

// Protection has to survive being cheaper: a token from the cookie is still
// required, still checked, and a wrong one is still refused.
func TestCookieStoredSecretStillProtectsUnsafeRequests(t *testing.T) {
	fixture := newCSRFStorageFixture(t, false)
	token := fixture.formToken(t)

	accepted, err := fixture.client.PostForm(
		fixture.server.URL+"/submit",
		url.Values{"csrf_token": {token}},
	)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Body.Close()
	if accepted.StatusCode != http.StatusNoContent {
		t.Fatalf("valid token status = %d, want %d", accepted.StatusCode, http.StatusNoContent)
	}

	refused, err := fixture.client.PostForm(
		fixture.server.URL+"/submit",
		url.Values{"csrf_token": {"not-the-token"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	refused.Body.Close()
	if refused.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid token status = %d, want %d", refused.StatusCode, http.StatusForbidden)
	}

	missing, err := fixture.client.PostForm(fixture.server.URL+"/submit", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusForbidden {
		t.Fatalf("absent token status = %d, want %d", missing.StatusCode, http.StatusForbidden)
	}
}

// A secret observed before a privilege change must not survive one, whichever
// side of the storage decision it lives on.
func TestLoginRotatesTheCSRFSecretInBothStorageModes(t *testing.T) {
	for name, useSessions := range map[string]bool{"cookie": false, "session": true} {
		t.Run(name, func(t *testing.T) {
			fixture := newCSRFStorageFixture(t, useSessions)
			before := fixture.formToken(t)
			beforeSecret, ok := unmaskCSRF(before)
			if !ok {
				t.Fatal("pre-login token does not unmask")
			}

			response, err := fixture.client.PostForm(
				fixture.server.URL+"/login",
				url.Values{"csrf_token": {before}},
			)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("login status = %d, want %d", response.StatusCode, http.StatusOK)
			}

			after := fixture.formToken(t)
			afterSecret, ok := unmaskCSRF(after)
			if !ok {
				t.Fatal("post-login token does not unmask")
			}
			if string(beforeSecret) == string(afterSecret) {
				t.Fatal("login did not rotate the CSRF secret")
			}
		})
	}
}

// Storing the secret in the session stays available for deployments that
// share a registrable domain with hosts they do not control.
func TestSessionStorageModeStillStoresTheSecretServerSide(t *testing.T) {
	fixture := newCSRFStorageFixture(t, true)

	if token := fixture.formToken(t); token == "" {
		t.Fatal("no CSRF token issued")
	}

	if stored := fixture.storedSessions(t); stored != 1 {
		t.Fatalf("session storage mode stored %d sessions, want 1", stored)
	}
}
