package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/bon5co/godjango/auth"
)

type SessionConfig struct {
	CookieName  string
	Domain      string
	Path        string
	Lifetime    time.Duration
	IdleTimeout time.Duration
	Secure      bool
	SameSite    http.SameSite
	Persistent  bool
}

type Sessions struct {
	manager *scs.SessionManager
}

var validCookieName = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

func NewSessions(config SessionConfig, store scs.Store) (*Sessions, error) {
	if !validCookieName.MatchString(config.CookieName) {
		return nil, errors.New("godjango web: valid session cookie name is required")
	}
	if config.Lifetime <= 0 {
		return nil, errors.New("godjango web: positive session lifetime is required")
	}
	if store == nil {
		return nil, errors.New("godjango web: session store is required")
	}
	path := config.Path
	if path == "" {
		path = "/"
	}
	sameSite := config.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	manager := scs.New()
	manager.Store = store
	manager.Lifetime = config.Lifetime
	manager.IdleTimeout = config.IdleTimeout
	manager.Cookie.Name = config.CookieName
	manager.Cookie.Domain = config.Domain
	manager.Cookie.HttpOnly = true
	manager.Cookie.Path = path
	manager.Cookie.Secure = config.Secure
	manager.Cookie.SameSite = sameSite
	manager.Cookie.Persist = config.Persistent
	return &Sessions{manager: manager}, nil
}

type requestSessionContextKey struct{}

func (sessions *Sessions) Middleware(next http.Handler) http.Handler {
	adapted := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := &requestSession{
			manager: sessions.manager,
			ctx:     request.Context(),
		}
		ctx := withRequestSession(request.Context(), current)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
	return sessions.manager.LoadAndSave(adapted)
}

func SessionFromRequest(request *http.Request) auth.Session {
	if request == nil {
		return nil
	}
	current, _ := request.Context().Value(requestSessionContextKey{}).(*requestSession)
	return current
}

func withRequestSession(ctx context.Context, session *requestSession) context.Context {
	return context.WithValue(ctx, requestSessionContextKey{}, session)
}

type requestSession struct {
	manager *scs.SessionManager
	ctx     context.Context
}

func (session *requestSession) ID() string {
	return session.manager.Token(session.ctx)
}

func (session *requestSession) Get(key string) (string, bool) {
	if !session.manager.Exists(session.ctx, key) {
		return "", false
	}
	return session.manager.GetString(session.ctx, key), true
}

func (session *requestSession) Put(key, value string) {
	session.manager.Put(session.ctx, key, value)
}

func (session *requestSession) Delete(key string) {
	session.manager.Remove(session.ctx, key)
}

func (session *requestSession) Cycle() error {
	if err := session.manager.RenewToken(session.ctx); err != nil {
		return err
	}
	return rotateCSRFState(session.ctx, session.manager)
}

func (session *requestSession) Flush() error {
	return session.manager.Destroy(session.ctx)
}
