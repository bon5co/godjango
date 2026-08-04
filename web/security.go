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

func TrustedProxy(trusted []netip.Prefix) Middleware {
	prefixes := append([]netip.Prefix(nil), trusted...)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			remote := addressFromRemoteAddr(request.RemoteAddr)
			client := remote
			if prefixContains(prefixes, remote) {
				forwarded, valid := forwardedAddresses(request.Header.Get("X-Forwarded-For"))
				if valid {
					for index := len(forwarded) - 1; index >= 0; index-- {
						if !prefixContains(prefixes, forwarded[index]) {
							client = forwarded[index]
							break
						}
					}
				}
			}
			ctx := context.WithValue(request.Context(), remoteIPContextKey{}, client)
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
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
