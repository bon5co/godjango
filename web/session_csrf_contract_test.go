package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2/memstore"
	"github.com/bon5co/godjango/auth"
)

func TestSessionCookieLifecycleAndLoginRotation(t *testing.T) {
	sessions, err := NewSessions(SessionConfig{
		CookieName: "godjango_session",
		Lifetime:   time.Hour,
		Secure:     true,
		SameSite:   http.SameSiteLaxMode,
	}, memstore.New())
	if err != nil {
		t.Fatal(err)
	}
	handler := sessions.Middleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		session := SessionFromRequest(request)
		switch request.URL.Path {
		case "/set":
			session.Put("cart", "book")
			response.WriteHeader(http.StatusNoContent)
		case "/get":
			value, _ := session.Get("cart")
			_, _ = io.WriteString(response, value)
		case "/id":
			_, _ = io.WriteString(response, session.ID())
		case "/login":
			user := &auth.User{ID: "user-1", PasswordHash: "encoded"}
			if err := auth.Login(session, user, []byte("secret")); err != nil {
				t.Fatal(err)
			}
			value, _ := session.Get("cart")
			response.Header().Set("X-Cart", value)
			_, _ = io.WriteString(response, session.ID())
		case "/logout":
			if err := auth.Logout(session); err != nil {
				t.Fatal(err)
			}
			response.WriteHeader(http.StatusNoContent)
		}
	}))
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	response, err := client.Get(server.URL + "/set")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	sessionCookie := namedCookie(t, response.Cookies(), "godjango_session")
	if !sessionCookie.Secure || !sessionCookie.HttpOnly ||
		sessionCookie.SameSite != http.SameSiteLaxMode ||
		sessionCookie.Path != "/" {
		t.Fatalf("session cookie = %+v", sessionCookie)
	}
	oldID := getBody(t, client, server.URL+"/id")
	if oldID == "" {
		t.Fatal("committed session ID is empty")
	}
	loginResponse, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	newIDBytes, err := io.ReadAll(loginResponse.Body)
	loginResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	newID := string(newIDBytes)
	if newID == "" || newID == oldID {
		t.Fatalf("session ID after login = %q, before = %q", newID, oldID)
	}
	if loginResponse.Header.Get("X-Cart") != "book" {
		t.Fatal("anonymous data was lost during login rotation")
	}

	oldCookieClient := server.Client()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/get", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(sessionCookie)
	oldResponse, err := oldCookieClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	oldBody, err := io.ReadAll(oldResponse.Body)
	oldResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(oldBody) != "" {
		t.Fatalf("rotated cookie still loaded %q", oldBody)
	}

	logoutResponse, err := client.Get(server.URL + "/logout")
	if err != nil {
		t.Fatal(err)
	}
	logoutResponse.Body.Close()
	expired := namedCookie(t, logoutResponse.Cookies(), "godjango_session")
	if expired.MaxAge >= 0 || !expired.Expires.Before(time.Now()) {
		t.Fatalf("logout cookie = %+v, want expired", expired)
	}
}

func TestCSRFMasksTokensRejectsFailuresAndRotatesOnLogin(t *testing.T) {
	sessions, err := NewSessions(SessionConfig{
		CookieName: "godjango_session",
		Lifetime:   time.Hour,
		Secure:     true,
		SameSite:   http.SameSiteLaxMode,
	}, memstore.New())
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := NewCSRF(CSRFConfig{
		CookieName: "godjango_csrf",
		Secure:     true,
		SameSite:   http.SameSiteLaxMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := BodyLimit(128)(sessions.Middleware(csrf.Middleware(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/token":
				first := CSRFToken(request)
				second := CSRFToken(request)
				response.Header().Set("X-Second-CSRF", second)
				_, _ = io.WriteString(response, first)
			case "/submit":
				response.WriteHeader(http.StatusNoContent)
			case "/login":
				user := &auth.User{ID: "user-1", PasswordHash: "encoded"}
				if err := auth.Login(SessionFromRequest(request), user, []byte("secret")); err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(response, CSRFToken(request))
			}
		},
	))))
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	tokenResponse, err := client.Get(server.URL + "/token")
	if err != nil {
		t.Fatal(err)
	}
	tokenBytes, err := io.ReadAll(tokenResponse.Body)
	tokenResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	token := string(tokenBytes)
	secondToken := tokenResponse.Header.Get("X-Second-CSRF")
	if token == "" || secondToken == "" || token == secondToken {
		t.Fatalf("masked tokens first=%q second=%q", token, secondToken)
	}
	csrfCookie := namedCookie(t, tokenResponse.Cookies(), "godjango_csrf")
	if !csrfCookie.Secure || csrfCookie.HttpOnly ||
		csrfCookie.SameSite != http.SameSiteLaxMode ||
		csrfCookie.Path != "/" {
		t.Fatalf("CSRF cookie = %+v", csrfCookie)
	}

	for _, test := range []struct {
		name   string
		token  string
		origin string
		status int
	}{
		{name: "missing", status: http.StatusForbidden},
		{name: "wrong", token: strings.Repeat("x", len(token)), status: http.StatusForbidden},
		{
			name:   "foreign origin",
			token:  token,
			origin: "https://evil.example",
			status: http.StatusForbidden,
		},
		{name: "same origin", token: token, origin: server.URL, status: http.StatusNoContent},
		{name: "valid", token: token, status: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL+"/submit", nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.token != "" {
				request.Header.Set("X-CSRF-Token", test.token)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}

	oversizedRequest, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/submit",
		struct{ io.Reader }{strings.NewReader("field=" + strings.Repeat("x", 256))},
	)
	if err != nil {
		t.Fatal(err)
	}
	oversizedRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	oversizedResponse, err := client.Do(oversizedRequest)
	if err != nil {
		t.Fatal(err)
	}
	oversizedResponse.Body.Close()
	if oversizedResponse.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized CSRF form status = %d, want 413", oversizedResponse.StatusCode)
	}

	loginRequest, err := http.NewRequest(http.MethodPost, server.URL+"/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	loginRequest.Header.Set("X-CSRF-Token", token)
	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		t.Fatal(err)
	}
	rotatedBytes, err := io.ReadAll(loginResponse.Body)
	loginResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	rotated := string(rotatedBytes)
	if rotated == "" || validCSRFPair(token, rotated) {
		t.Fatalf("CSRF secret did not rotate on login: before=%q after=%q", token, rotated)
	}
}

// Every deployed application sits behind a proxy that terminates TLS, so the
// connection reaching this process is plaintext while the browser still sends
// an https Origin. Validating that Origin against the connection rejected every
// POST form in production while passing locally over plain HTTP; the forwarded
// scheme is what makes the two agree, and only from a peer that was declared
// trustworthy.
func TestCSRFOriginFollowsTheForwardedSchemeFromATrustedProxy(t *testing.T) {
	const serverOrigin = "SERVER_ORIGIN"
	for _, test := range []struct {
		name      string
		trust     TrustedProxyConfig
		forwarded string
		origin    string
		status    int
	}{
		{
			name:      "https origin through a trusted proxy",
			trust:     TrustedProxyConfig{TrustAnyPeer: true},
			forwarded: "https",
			origin:    "https://" + serverOrigin,
			status:    http.StatusNoContent,
		},
		{
			name:      "another site's origin is still refused",
			trust:     TrustedProxyConfig{TrustAnyPeer: true},
			forwarded: "https",
			origin:    "https://evil.example",
			status:    http.StatusForbidden,
		},
		{
			name:      "an http origin no longer matches an https deployment",
			trust:     TrustedProxyConfig{TrustAnyPeer: true},
			forwarded: "https",
			origin:    "http://" + serverOrigin,
			status:    http.StatusForbidden,
		},
		{
			name:      "the header is ignored where no proxy is trusted",
			forwarded: "https",
			origin:    "https://" + serverOrigin,
			status:    http.StatusForbidden,
		},
		{
			name:   "a direct plaintext deployment still validates its own origin",
			origin: "http://" + serverOrigin,
			status: http.StatusNoContent,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			// The cookies are left unmarked here only because Go's cookie jar
			// refuses Secure cookies over the plaintext hop this test is
			// reproducing. A real browser is on https and keeps them.
			handler := Chain(
				http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
					switch request.URL.Path {
					case "/token":
						_, _ = io.WriteString(response, CSRFToken(request))
					case "/submit":
						response.WriteHeader(http.StatusNoContent)
					}
				}),
				TrustedProxy(test.trust),
				sessions.Middleware,
				csrf.Middleware,
			)
			server := httptest.NewServer(handler)
			defer server.Close()
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			client := server.Client()
			client.Jar = jar
			host := mustURL(t, server.URL).Host

			tokenRequest, err := http.NewRequest(http.MethodGet, server.URL+"/token", nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.forwarded != "" {
				tokenRequest.Header.Set("X-Forwarded-Proto", test.forwarded)
			}
			tokenResponse, err := client.Do(tokenRequest)
			if err != nil {
				t.Fatal(err)
			}
			tokenBytes, err := io.ReadAll(tokenResponse.Body)
			tokenResponse.Body.Close()
			if err != nil {
				t.Fatal(err)
			}

			request, err := http.NewRequest(http.MethodPost, server.URL+"/submit", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("X-CSRF-Token", string(tokenBytes))
			request.Header.Set("Origin", strings.ReplaceAll(test.origin, serverOrigin, host))
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwarded)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}

func namedCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in %+v", name, cookies)
	return nil
}

func getBody(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
