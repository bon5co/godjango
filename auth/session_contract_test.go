// Adapted from Django tests/auth_tests/test_login.py and django/contrib/auth
// login/logout tests at commit 274a1d4.
// See THIRD_PARTY_NOTICES.md.
package auth_test

import (
	"fmt"
	"testing"

	"github.com/bon5co/godjango/auth"
)

type fakeSession struct {
	id      string
	values  map[string]string
	cycles  int
	flushes int
}

func newFakeSession(id string) *fakeSession {
	return &fakeSession{id: id, values: make(map[string]string)}
}

func (s *fakeSession) ID() string {
	return s.id
}

func (s *fakeSession) Get(key string) (string, bool) {
	value, ok := s.values[key]
	return value, ok
}

func (s *fakeSession) Put(key, value string) {
	s.values[key] = value
}

func (s *fakeSession) Delete(key string) {
	delete(s.values, key)
}

func (s *fakeSession) Cycle() error {
	s.cycles++
	s.id = fmt.Sprintf("cycled-%d", s.cycles)
	return nil
}

func (s *fakeSession) Flush() error {
	s.flushes++
	s.id = fmt.Sprintf("flushed-%d", s.flushes)
	s.values = make(map[string]string)
	return nil
}

// Django: test_login.py::TestLogin::test_user_login and
// django/contrib/auth/__init__.py::login anonymous-session branch.
func TestLoginCyclesAnonymousSessionAndStoresUser(t *testing.T) {
	session := newFakeSession("anonymous")
	session.Put("cart", "retained")
	user := &auth.User{ID: "user-1", PasswordHash: "encoded"}

	if err := auth.Login(session, user, []byte("secret")); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.cycles != 1 {
		t.Errorf("Cycle() calls = %d, want 1", session.cycles)
	}
	if session.flushes != 0 {
		t.Errorf("Flush() calls = %d, want 0", session.flushes)
	}
	if got, _ := session.Get("cart"); got != "retained" {
		t.Errorf("anonymous session data lost: cart = %q", got)
	}
	if got, _ := session.Get(auth.SessionUserIDKey); got != user.ID {
		t.Errorf("session user id = %q, want %q", got, user.ID)
	}
}

// Django: test_login.py::TestLogin::test_inactive_user.
func TestExplicitLoginAllowsInactiveUser(t *testing.T) {
	session := newFakeSession("anonymous")
	user := &auth.User{ID: "inactive", IsActive: false, PasswordHash: "encoded"}

	if err := auth.Login(session, user, []byte("secret")); err != nil {
		t.Fatalf("Login(inactive) error = %v", err)
	}
	if got, _ := session.Get(auth.SessionUserIDKey); got != user.ID {
		t.Errorf("session user id = %q, want %q", got, user.ID)
	}
}

// Django: test_views.py::LoginTest::test_session_key_flushed_on_login.
func TestLoginAsDifferentUserFlushesSession(t *testing.T) {
	session := newFakeSession("authenticated")
	session.Put(auth.SessionUserIDKey, "old-user")
	session.Put("private", "old-user-data")
	user := &auth.User{ID: "new-user", PasswordHash: "encoded"}

	if err := auth.Login(session, user, []byte("secret")); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.flushes != 1 {
		t.Errorf("Flush() calls = %d, want 1", session.flushes)
	}
	if _, ok := session.Get("private"); ok {
		t.Error("old user's private session data survived")
	}
	if got, _ := session.Get(auth.SessionUserIDKey); got != user.ID {
		t.Errorf("session user id = %q, want %q", got, user.ID)
	}
}

// Django: test_views.py::LogoutTest::test_logout_default.
func TestLogoutFlushesSession(t *testing.T) {
	session := newFakeSession("authenticated")
	session.Put(auth.SessionUserIDKey, "user-1")
	session.Put("private", "data")

	if err := auth.Logout(session); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if session.flushes != 1 {
		t.Errorf("Flush() calls = %d, want 1", session.flushes)
	}
	if len(session.values) != 0 {
		t.Errorf("session after logout = %v, want empty", session.values)
	}
}

// Django: test_middleware.py::TestAuthenticationMiddleware::
// test_changed_password_invalidates_session.
func TestPasswordChangeInvalidatesExistingSession(t *testing.T) {
	secret := []byte("secret")
	session := newFakeSession("authenticated")
	user := &auth.User{ID: "user-1", PasswordHash: "old-encoded"}

	if err := auth.Login(session, user, secret); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	user.PasswordHash = "new-encoded"
	if id, ok := auth.SessionUserID(session, user, secret); ok {
		t.Fatalf("SessionUserID() = %q, true after password change", id)
	}
}
