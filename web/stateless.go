package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// StatelessPaths declares the request paths a deployment serves without
// per-request session state: no session load or save, no CSRF secret, no
// authenticated user. It exists because the stateful chain is not free. Every
// request that reaches the session and CSRF middleware without a cookie is
// issued one, and issuing one writes a row, so an unauthenticated JSON
// endpoint under sustained load writes a session row per request and pays a
// database round trip for state no caller asked for.
//
// A prefix matches the request path exactly or at a segment boundary, so
// "/api" covers "/api" and "/api/ping" and does not cover "/apiary". Prefixes
// are matched against the cleaned request path, so "/api/../admin" is matched
// as "/admin" and stays stateful.
//
// Exempting a path removes authentication from it as well as session storage.
// Handlers under a stateless prefix always observe an anonymous request:
// CurrentUser reports no user, CSRFToken returns the empty string, and
// RequireAuthentication and RequirePermission refuse the request. Anything a
// stateless route needs to authorize has to travel in the request itself.
type StatelessPaths []string

// Exempt wraps middleware so it runs only for requests outside the declared
// prefixes. The wrapped middleware is skipped entirely for stateless requests
// rather than being given a request it would have to special-case, which keeps
// the decision in one place instead of inside session, CSRF and authentication
// separately.
func (paths StatelessPaths) Exempt(middleware Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		if middleware == nil {
			return next
		}
		wrapped := middleware(next)
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if paths.Match(request) {
				next.ServeHTTP(response, markStateless(request))
				return
			}
			wrapped.ServeHTTP(response, request)
		})
	}
}

// Match reports whether a request is served without per-request state.
func (paths StatelessPaths) Match(request *http.Request) bool {
	if request == nil {
		return false
	}
	path := requestPath(request)
	for _, prefix := range paths {
		if matchesPrefix(path, normalizePrefix(prefix)) {
			return true
		}
	}
	return false
}

// Validate reports whether every declared prefix is usable. A prefix that does
// not start at the root, or that is empty, silently matches nothing or matches
// everything, so servers reject it at startup rather than at traffic.
func (paths StatelessPaths) Validate() error {
	for _, prefix := range paths {
		trimmed := strings.TrimSpace(prefix)
		if trimmed == "" {
			return errors.New("godjango web: stateless path prefix is empty")
		}
		if !strings.HasPrefix(trimmed, "/") {
			return errors.New("godjango web: stateless path prefix must start with /: " + prefix)
		}
		if normalizePrefix(trimmed) == "/" {
			return errors.New("godjango web: stateless path prefix / would exempt every route")
		}
	}
	return nil
}

type statelessContextKey struct{}

// IsStateless reports whether the request was served without session, CSRF or
// authentication state. Handlers that are mounted both inside and outside a
// stateless prefix use it to tell which chain they are running under.
func IsStateless(request *http.Request) bool {
	if request == nil {
		return false
	}
	marked, _ := request.Context().Value(statelessContextKey{}).(bool)
	return marked
}

func markStateless(request *http.Request) *http.Request {
	if IsStateless(request) {
		return request
	}
	return request.WithContext(context.WithValue(request.Context(), statelessContextKey{}, true))
}

func requestPath(request *http.Request) string {
	path := request.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	// CleanPath collapses "." and ".." and duplicate separators, so a caller
	// cannot reach a stateful route through a prefix that looks stateless.
	return cleanPath(path)
}

func cleanPath(path string) string {
	cleaned := path
	for strings.Contains(cleaned, "//") {
		cleaned = strings.ReplaceAll(cleaned, "//", "/")
	}
	segments := strings.Split(cleaned, "/")
	resolved := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
		default:
			resolved = append(resolved, segment)
		}
	}
	trailing := strings.HasSuffix(cleaned, "/") && len(resolved) > 0
	result := "/" + strings.Join(resolved, "/")
	if trailing {
		result += "/"
	}
	return result
}

func normalizePrefix(prefix string) string {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "" {
		return ""
	}
	cleaned := cleanPath(trimmed)
	if cleaned != "/" {
		cleaned = strings.TrimSuffix(cleaned, "/")
	}
	return cleaned
}

func matchesPrefix(path, prefix string) bool {
	if prefix == "" {
		return false
	}
	if prefix == "/" {
		return true
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}
