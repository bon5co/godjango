// Package requestscheme holds the scheme a request's client actually used.
//
// It exists because two packages need the same answer and neither may import
// the other: web decides it (TrustedProxy weighs the forwarding headers against
// the declared trust boundary and stores the result) and web/view reads it (an
// og:image or a canonical URL has to be absolute, and half of an absolute URL is
// the scheme). web already imports web/view to render, so the storage sits below
// both rather than being derived a second time in the renderer -- a second
// derivation would read request.TLS, which is exactly the plaintext-behind-a-
// proxy mistake that produced https pages advertising http URLs.
//
// The public API stays web.RequestScheme and web.RequestIsHTTPS; this package is
// internal and applications never see it.
package requestscheme

import (
	"context"
	"net/http"
)

const (
	HTTP  = "http"
	HTTPS = "https"
)

type contextKey struct{}

// With stores the scheme the client used, which only the trust boundary in web
// is in a position to decide.
func With(ctx context.Context, scheme string) context.Context {
	return context.WithValue(ctx, contextKey{}, scheme)
}

// Of reports the client's scheme, falling back to the connection this process
// accepted when nothing decided otherwise. A request that never passed through
// TrustedProxy is therefore reported as plaintext even when the browser is on
// https, which is the safe direction for a security decision and the wrong one
// for a link a scraper will follow -- see the caller-facing note on
// web.RequestScheme.
func Of(request *http.Request) string {
	if request == nil {
		return HTTP
	}
	if scheme, ok := request.Context().Value(contextKey{}).(string); ok {
		return scheme
	}
	return Observed(request)
}

// Observed is the scheme of the connection this process accepted, which is all
// net/http can determine without being told.
func Observed(request *http.Request) string {
	if request.TLS != nil {
		return HTTPS
	}
	return HTTP
}
