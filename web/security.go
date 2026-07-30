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
	HTTPS                 bool
	ContentSecurityPolicy string
}

func SecurityHeaders(config SecurityHeadersConfig) Middleware {
	contentSecurityPolicy := config.ContentSecurityPolicy
	if contentSecurityPolicy == "" {
		contentSecurityPolicy = "default-src 'self'"
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
				forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
				if len(forwarded) > 0 {
					if parsed, err := netip.ParseAddr(strings.TrimSpace(forwarded[0])); err == nil {
						client = parsed.Unmap()
					}
				}
			}
			ctx := context.WithValue(request.Context(), remoteIPContextKey{}, client)
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
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
