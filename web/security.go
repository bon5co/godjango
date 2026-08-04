// Package web provides GoDjangGo's net/http runtime and security middleware.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type Middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middleware ...Middleware) http.Handler {
	for index := len(middleware) - 1; index >= 0; index-- {
		handler = middleware[index](handler)
	}
	return handler
}

func IsSafeRedirect(
	request *http.Request,
	target string,
	allowedHosts []string,
	requireHTTPS bool,
) bool {
	if request == nil || target == "" || strings.Contains(target, `\`) {
		return false
	}
	for _, character := range target {
		if unicode.IsControl(character) {
			return false
		}
		break
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if requireHTTPS && parsed.Scheme != "" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Host == "" {
		return parsed.Scheme == "" && !strings.HasPrefix(parsed.Path, "//")
	}
	allowed := make(map[string]struct{}, len(allowedHosts)+1)
	allowed[strings.ToLower(request.Host)] = struct{}{}
	for _, host := range allowedHosts {
		allowed[strings.ToLower(host)] = struct{}{}
	}
	_, ok := allowed[strings.ToLower(parsed.Host)]
	return ok
}

type requestIDContextKey struct{}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			requestID := request.Header.Get("X-Request-ID")
			if !validRequestID.MatchString(requestID) {
				random := make([]byte, 16)
				if _, err := rand.Read(random); err != nil {
					panic(fmt.Errorf("godjango web: generate request ID: %w", err))
				}
				requestID = hex.EncodeToString(random)
			}
			response.Header().Set("X-Request-ID", requestID)
			ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
}

func RequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					slog.ErrorContext(
						request.Context(),
						"recovered HTTP panic",
						"request_id",
						RequestIDFromContext(request.Context()),
						"panic_type",
						fmt.Sprintf("%T", recovered),
					)
					WriteError(
						response,
						request,
						http.StatusInternalServerError,
						"internal_error",
					)
				}
			}()
			next.ServeHTTP(response, request)
		})
	}
}

func WriteError(response http.ResponseWriter, request *http.Request, status int, code string) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{
		"code":       code,
		"request_id": RequestIDFromContext(request.Context()),
	})
}

type SecurityHeadersConfig struct {
	HTTPS bool
	// ContentSecurityPolicy replaces the default policy wholesale. Setting it
	// takes over responsibility for every directive, so ConnectSources is
	// refused alongside it rather than silently ignored.
	ContentSecurityPolicy string
	// ConnectSources widens the default policy by exactly one directive:
	// connect-src. An application whose page must call a named third-party
	// origin from the browser -- an upstream API it is reporting on, not an
	// asset host -- names those origins here instead of restating the whole
	// policy and drifting from the default as it changes. Everything else stays
	// at default-src 'self', so scripts, styles and frames do not widen with it.
	ConnectSources []string
}

// SecurityHeaders panics on a configuration that cannot mean what it says. A
// policy silently missing the origins an application declared would fail as a
// blocked request in the browser, at a distance from the mistake.
func SecurityHeaders(config SecurityHeadersConfig) Middleware {
	contentSecurityPolicy := config.ContentSecurityPolicy
	if contentSecurityPolicy != "" && len(config.ConnectSources) > 0 {
		panic("godjango web: ContentSecurityPolicy replaces the whole policy; " +
			"declare connect-src inside it rather than in ConnectSources")
	}
	if contentSecurityPolicy == "" {
		contentSecurityPolicy = defaultContentSecurityPolicy(config.ConnectSources)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			headers := response.Header()
			headers.Set("Content-Security-Policy", contentSecurityPolicy)
			headers.Set("Referrer-Policy", "same-origin")
			headers.Set("X-Content-Type-Options", "nosniff")
			headers.Set("X-Frame-Options", "DENY")
			headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if config.HTTPS {
				headers.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(response, request)
		})
	}
}

// defaultContentSecurityPolicy is default-src 'self' plus, when an application
// declared them, a connect-src directive listing 'self' and those origins. The
// directive repeats 'self' because connect-src overrides default-src for that
// fetch type rather than adding to it: omitting it would take the application's
// own API calls away in exchange for reaching a third party.
func defaultContentSecurityPolicy(connectSources []string) string {
	const base = "default-src 'self'"
	if len(connectSources) == 0 {
		return base
	}
	sources := make([]string, 0, len(connectSources)+1)
	sources = append(sources, "'self'")
	for _, source := range connectSources {
		trimmed := strings.TrimSpace(source)
		if !validConnectSource(trimmed) {
			panic("godjango web: connect-src source must be scheme://host[:port], got " + source)
		}
		sources = append(sources, trimmed)
	}
	return base + "; connect-src " + strings.Join(sources, " ")
}

// validConnectSource accepts only a plain origin. A path, a wildcard host or
// anything carrying a delimiter would either be ignored by the browser or
// rewrite the neighbouring directives, and both fail far from the config that
// caused them.
func validConnectSource(source string) bool {
	if source == "" || strings.ContainsAny(source, " \t;,'\"") {
		return false
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return !strings.Contains(parsed.Host, "*")
}

func BodyLimit(bytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if bytes <= 0 {
				WriteError(response, request, http.StatusInternalServerError, "invalid_body_limit")
				return
			}
			if request.ContentLength > bytes {
				WriteError(response, request, http.StatusRequestEntityTooLarge, "body_too_large")
				return
			}
			request.Body = http.MaxBytesReader(response, request.Body, bytes)
			next.ServeHTTP(response, request)
		})
	}
}

type remoteIPContextKey struct{}

type requestSchemeContextKey struct{}

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// TrustedProxyConfig declares which immediate peers are allowed to speak for
// the client. A reverse proxy rewrites both facts an application cares about:
// the address it accepted the connection from is the proxy's, and the scheme it
// was reached by is plaintext HTTP whenever the proxy terminated TLS. The proxy
// restates the originals in X-Forwarded-For and X-Forwarded-Proto, and this is
// where an application says those restatements can be believed. Believing them
// unconditionally would let any client name its own address and scheme, so the
// default -- an unconfigured application -- believes nothing.
type TrustedProxyConfig struct {
	// Networks are the peer addresses whose forwarding headers are believed.
	// A request arriving from outside them keeps the address and scheme this
	// process observed, so a client that invents the headers speaks only for
	// itself.
	Networks []netip.Prefix

	// TrustAnyPeer believes forwarding headers from whatever address connected.
	// It exists for platforms that put the application behind their own proxy on
	// a private network without promising a fixed proxy address -- Dokploy,
	// Railway, Fly -- where the guarantee is topological rather than numeric:
	// nothing except the proxy can reach the port at all. With no Networks
	// declared the client address is read as the last X-Forwarded-For entry, the
	// nearest hop, because no entry can be ruled out as infrastructure.
	//
	// Enabling this on a port that is reachable directly from the internet hands
	// every request's identity to whoever sent it. RemoteIP then reports the
	// address the caller typed, so rate limits, audit logs and IP allowlists all
	// read a value an attacker chose, and RequestScheme reports https over a
	// plaintext connection. Leave it off unless the port is private.
	TrustAnyPeer bool
}

// TrustedProxy resolves, once per request, the two facts a reverse proxy
// obscures: the address the client connected from and the scheme it used. Both
// are decided here so the trust boundary is a single place in the source rather
// than a header read repeated in every middleware that needs one of them.
func TrustedProxy(config TrustedProxyConfig) Middleware {
	prefixes := append([]netip.Prefix(nil), config.Networks...)
	trustAnyPeer := config.TrustAnyPeer
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			remote := addressFromRemoteAddr(request.RemoteAddr)
			client := remote
			scheme := observedScheme(request)
			if trustAnyPeer || prefixContains(prefixes, remote) {
				forwarded, valid := forwardedAddresses(request.Header.Get("X-Forwarded-For"))
				if valid {
					for index := len(forwarded) - 1; index >= 0; index-- {
						if !prefixContains(prefixes, forwarded[index]) {
							client = forwarded[index]
							break
						}
					}
				}
				if forwarded, ok := forwardedScheme(
					request.Header.Get("X-Forwarded-Proto"),
				); ok {
					scheme = forwarded
				}
			}
			ctx := context.WithValue(request.Context(), remoteIPContextKey{}, client)
			ctx = context.WithValue(ctx, requestSchemeContextKey{}, scheme)
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
}

// forwardedScheme reads one X-Forwarded-Proto value. Only http and https are
// accepted, so a malformed or unexpected value leaves the observed scheme
// standing instead of becoming the answer by assignment. A proxy chain appends
// to the header, so the leftmost entry is the scheme the browser used.
func forwardedScheme(header string) (string, bool) {
	first, _, _ := strings.Cut(header, ",")
	switch strings.ToLower(strings.TrimSpace(first)) {
	case schemeHTTPS:
		return schemeHTTPS, true
	case schemeHTTP:
		return schemeHTTP, true
	default:
		return "", false
	}
}

// observedScheme is the scheme of the connection this process accepted, which
// is all net/http can determine without being told.
func observedScheme(request *http.Request) string {
	if request.TLS != nil {
		return schemeHTTPS
	}
	return schemeHTTP
}

// RequestScheme reports the scheme the client used, which behind a
// TLS-terminating proxy is not the scheme this process was reached by: the
// connection here is plaintext while the browser is on https. It answers with
// the forwarded scheme only where TrustedProxy accepted the peer; without that
// middleware, or from an untrusted peer, it answers with the connection this
// process actually accepted.
func RequestScheme(request *http.Request) string {
	if request == nil {
		return schemeHTTP
	}
	if scheme, ok := request.Context().Value(requestSchemeContextKey{}).(string); ok {
		return scheme
	}
	return observedScheme(request)
}

// RequestIsHTTPS reports whether the client's own connection was encrypted,
// including the case where a trusted proxy terminated that encryption.
func RequestIsHTTPS(request *http.Request) bool {
	return RequestScheme(request) == schemeHTTPS
}

func forwardedAddresses(header string) ([]netip.Addr, bool) {
	if strings.TrimSpace(header) == "" {
		return nil, false
	}
	parts := strings.Split(header, ",")
	addresses := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return nil, false
		}
		addresses = append(addresses, address.Unmap())
	}
	return addresses, true
}

func RemoteIP(request *http.Request) netip.Addr {
	if request == nil {
		return netip.Addr{}
	}
	if address, ok := request.Context().Value(remoteIPContextKey{}).(netip.Addr); ok {
		return address
	}
	return addressFromRemoteAddr(request.RemoteAddr)
}

func addressFromRemoteAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	address, _ := netip.ParseAddr(strings.Trim(host, "[]"))
	return address.Unmap()
}

func prefixContains(prefixes []netip.Prefix, address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
