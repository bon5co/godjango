package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
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

	// UseSessions keeps the CSRF secret in server-side session storage
	// instead of in the CSRF cookie.
	//
	// The default is the cookie, which is also Django's default, because
	// the session is the expensive place to put it. A secret that lives in
	// the session has to exist before a form can be rendered, so the first
	// anonymous request to any page creates a session and writes a row, and
	// sustained anonymous traffic grows the session table without bound for
	// state no caller ever uses. A secret that lives in its own cookie costs
	// nothing to issue and nothing to store.
	//
	// Both modes mask the secret per response and validate by unmasking, and
	// both refuse an unsafe request whose Origin does not match. The cookie
	// mode additionally relies on that Origin check to close the gap a plain
	// double-submit cookie leaves: a sibling host that can write cookies for
	// the parent domain can plant a secret, but it cannot make the browser
	// send a matching Origin. Applications that share a registrable domain
	// with hosts they do not control, and terminate their own TLS, should
	// set UseSessions and pay the storage.
	UseSessions bool
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
	secret []byte
	store  csrfSecretStore
}

func (state *csrfState) rotate() error {
	secret, err := randomCSRFSecret()
	if err != nil {
		return err
	}
	if err := state.store.save(secret); err != nil {
		return err
	}
	state.secret = secret
	return nil
}

// csrfSecretStore is where one request's CSRF secret is read from and written
// back to. It is the only difference between the two modes.
type csrfSecretStore interface {
	load() ([]byte, bool)
	save(secret []byte) error
}

// sessionCSRFStore keeps the secret in server-side session storage, so saving
// it marks the session modified and writes a row.
type sessionCSRFStore struct {
	manager *scs.SessionManager
	ctx     context.Context
}

func (store sessionCSRFStore) load() ([]byte, bool) {
	if !store.manager.Exists(store.ctx, csrfSessionKey) {
		return nil, false
	}
	secret, err := base64.RawURLEncoding.DecodeString(store.manager.GetString(store.ctx, csrfSessionKey))
	if err != nil || len(secret) != csrfBytes {
		return nil, false
	}
	return secret, true
}

func (store sessionCSRFStore) save(secret []byte) error {
	store.manager.Put(store.ctx, csrfSessionKey, base64.RawURLEncoding.EncodeToString(secret))
	return nil
}

// cookieCSRFStore keeps the secret in the CSRF cookie itself, masked the same
// way the exposed token is. Nothing is stored server-side, so an anonymous
// request costs no session and no row.
type cookieCSRFStore struct {
	request  *http.Request
	response http.ResponseWriter
	config   CSRFConfig
}

func (store cookieCSRFStore) load() ([]byte, bool) {
	cookie, err := store.request.Cookie(store.config.CookieName)
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	return unmaskCSRF(cookie.Value)
}

func (store cookieCSRFStore) save(secret []byte) error {
	masked, err := maskCSRF(secret)
	if err != nil {
		return err
	}
	http.SetCookie(store.response, &http.Cookie{
		Name:     store.config.CookieName,
		Value:    masked,
		Path:     store.config.Path,
		Domain:   store.config.Domain,
		Secure:   store.config.Secure,
		HttpOnly: false,
		SameSite: store.config.SameSite,
	})
	return nil
}

func (csrf *CSRF) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current, _ := SessionFromRequest(request).(*requestSession)
		if csrf.config.UseSessions && current == nil {
			WriteError(response, request, http.StatusInternalServerError, "session_unavailable")
			return
		}

		var store csrfSecretStore
		if csrf.config.UseSessions {
			store = sessionCSRFStore{manager: current.manager, ctx: current.ctx}
		} else {
			store = cookieCSRFStore{request: request, response: response, config: csrf.config}
		}

		secret, found := store.load()
		state := &csrfState{secret: secret, store: store}
		if !found {
			if err := state.rotate(); err != nil {
				WriteError(response, request, http.StatusInternalServerError, "csrf_unavailable")
				return
			}
		} else if err := store.save(state.secret); err != nil {
			// Re-issuing the secret refreshes the cookie's mask and its
			// expiry. In session mode it is the value already stored, so no
			// row is written.
			WriteError(response, request, http.StatusInternalServerError, "csrf_unavailable")
			return
		}

		ctx := context.WithValue(request.Context(), csrfContextKey{}, state)
		request = request.WithContext(ctx)
		if current != nil {
			current.ctx = ctx
		}

		if !safeMethod(request.Method) {
			if !validRequestOrigin(request) {
				WriteError(response, request, http.StatusForbidden, "csrf_failed")
				return
			}
			presented := request.Header.Get("X-CSRF-Token")
			if presented == "" {
				if err := request.ParseForm(); err != nil {
					var limitErr *http.MaxBytesError
					if errors.As(err, &limitErr) {
						WriteError(
							response,
							request,
							http.StatusRequestEntityTooLarge,
							"body_too_large",
						)
					} else {
						WriteError(response, request, http.StatusBadRequest, "invalid_form")
					}
					return
				}
				presented = request.PostForm.Get("csrf_token")
			}
			if !validCSRFToken(state.secret, presented) {
				WriteError(response, request, http.StatusForbidden, "csrf_failed")
				return
			}
		}
		next.ServeHTTP(response, request)
	})
}

func validRequestOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" {
		return false
	}
	// The comparison is against the client's scheme, not this process's: behind
	// a TLS-terminating proxy the connection here is plaintext while the browser
	// sends Origin: https://..., and checking the connection would reject every
	// form the application serves. X-Forwarded-Proto is safe to rely on for this
	// decision because a cross-origin script cannot set it -- it is not a
	// CORS-safelisted request header, so the browser refuses to send it -- but
	// it is still only read from a peer the application declared trusted. See
	// TrustedProxyConfig.
	return parsed.Scheme == RequestScheme(request) &&
		strings.EqualFold(parsed.Host, request.Host)
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

// rotateCSRFState issues a new secret for the request in flight. Login,
// logout and password change call it through Session.Cycle so a secret
// observed before a privilege change cannot be replayed after one.
//
// A request that never reached the CSRF middleware has no state to rotate.
// That is the case for a stateless path, where there is no CSRF secret to
// replay either, so rotation succeeds by having nothing to do.
func rotateCSRFState(ctx context.Context) error {
	state, ok := ctx.Value(csrfContextKey{}).(*csrfState)
	if !ok {
		return nil
	}
	return state.rotate()
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
