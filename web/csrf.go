package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/alexedwards/scs/v2"
)

const (
	csrfSessionKey = "_godjango_csrf_secret"
	csrfBytes      = 32
)

type CSRFConfig struct {
	CookieName string
	Domain     string
	Path       string
	Secure     bool
	SameSite   http.SameSite
}

type CSRF struct {
	config CSRFConfig
}

func NewCSRF(config CSRFConfig) (*CSRF, error) {
	if !validCookieName.MatchString(config.CookieName) {
		return nil, errors.New("godjango web: valid CSRF cookie name is required")
	}
	if config.Path == "" {
		config.Path = "/"
	}
	if config.SameSite == 0 {
		config.SameSite = http.SameSiteLaxMode
	}
	return &CSRF{config: config}, nil
}

type csrfContextKey struct{}

type csrfState struct {
	manager *scs.SessionManager
	ctx     context.Context
	secret  []byte
}

func (csrf *CSRF) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		session := SessionFromRequest(request)
		current, ok := session.(*requestSession)
		if !ok {
			WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
			return
		}
		secret, err := ensureCSRFSecret(current.ctx, current.manager)
		if err != nil {
			WriteError(response, request, http.StatusInternalServerError, "csrf_unavailable")
			return
		}
		state := &csrfState{
			manager: current.manager,
			ctx:     current.ctx,
			secret:  secret,
		}
		ctx := context.WithValue(request.Context(), csrfContextKey{}, state)
		request = request.WithContext(ctx)
		current.ctx = ctx

		cookieToken, err := maskCSRF(secret)
		if err != nil {
			WriteError(response, request, http.StatusInternalServerError, "csrf_unavailable")
			return
		}
		http.SetCookie(response, &http.Cookie{
			Name:     csrf.config.CookieName,
			Value:    cookieToken,
			Path:     csrf.config.Path,
			Domain:   csrf.config.Domain,
			Secure:   csrf.config.Secure,
			HttpOnly: false,
			SameSite: csrf.config.SameSite,
		})

		if !safeMethod(request.Method) {
			presented := request.Header.Get("X-CSRF-Token")
			if presented == "" {
				if err := request.ParseForm(); err == nil {
					presented = request.PostForm.Get("csrf_token")
				}
			}
			if !validCSRFToken(secret, presented) {
				WriteError(response, request, http.StatusForbidden, "csrf_failed")
				return
			}
		}
		next.ServeHTTP(response, request)
	})
}

func CSRFToken(request *http.Request) string {
	if request == nil {
		return ""
	}
	state, _ := request.Context().Value(csrfContextKey{}).(*csrfState)
	if state == nil {
		return ""
	}
	token, err := maskCSRF(state.secret)
	if err != nil {
		return ""
	}
	return token
}

func rotateCSRFState(ctx context.Context, manager *scs.SessionManager) error {
	secret, err := randomCSRFSecret()
	if err != nil {
		return err
	}
	manager.Put(ctx, csrfSessionKey, base64.RawURLEncoding.EncodeToString(secret))
	if state, ok := ctx.Value(csrfContextKey{}).(*csrfState); ok {
		state.secret = secret
	}
	return nil
}

func ensureCSRFSecret(ctx context.Context, manager *scs.SessionManager) ([]byte, error) {
	if manager.Exists(ctx, csrfSessionKey) {
		encoded := manager.GetString(ctx, csrfSessionKey)
		secret, err := base64.RawURLEncoding.DecodeString(encoded)
		if err == nil && len(secret) == csrfBytes {
			return secret, nil
		}
	}
	secret, err := randomCSRFSecret()
	if err != nil {
		return nil, err
	}
	manager.Put(ctx, csrfSessionKey, base64.RawURLEncoding.EncodeToString(secret))
	return secret, nil
}

func randomCSRFSecret() ([]byte, error) {
	secret := make([]byte, csrfBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func maskCSRF(secret []byte) (string, error) {
	if len(secret) != csrfBytes {
		return "", errors.New("godjango web: invalid CSRF secret")
	}
	mask := make([]byte, csrfBytes)
	if _, err := rand.Read(mask); err != nil {
		return "", err
	}
	masked := make([]byte, csrfBytes*2)
	copy(masked, mask)
	for index := range secret {
		masked[csrfBytes+index] = mask[index] ^ secret[index]
	}
	return base64.RawURLEncoding.EncodeToString(masked), nil
}

func unmaskCSRF(token string) ([]byte, bool) {
	if !csrfTokenPattern.MatchString(token) {
		return nil, false
	}
	masked, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(masked) != csrfBytes*2 {
		return nil, false
	}
	secret := make([]byte, csrfBytes)
	for index := range secret {
		secret[index] = masked[index] ^ masked[csrfBytes+index]
	}
	return secret, true
}

var csrfTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{86}$`)

func validCSRFToken(secret []byte, token string) bool {
	presented, ok := unmaskCSRF(strings.TrimSpace(token))
	return ok && subtle.ConstantTimeCompare(secret, presented) == 1
}

func validCSRFPair(first, second string) bool {
	firstSecret, firstOK := unmaskCSRF(first)
	secondSecret, secondOK := unmaskCSRF(second)
	return firstOK &&
		secondOK &&
		subtle.ConstantTimeCompare(firstSecret, secondSecret) == 1
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
