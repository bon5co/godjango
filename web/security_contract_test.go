package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestSafeRedirectMatchesDjangoSecuritySemantics(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://example.com/login", nil)
	tests := []struct {
		name   string
		target string
		safe   bool
	}{
		{name: "relative", target: "/account", safe: true},
		{name: "same host https", target: "https://example.com/account", safe: true},
		{name: "allowed host", target: "https://admin.example.com/account", safe: true},
		{name: "foreign host", target: "https://evil.example/account", safe: false},
		{name: "scheme relative foreign host", target: "//evil.example/account", safe: false},
		{name: "downgrade", target: "http://example.com/account", safe: false},
		{name: "backslash authority", target: `\\evil.example\account`, safe: false},
		{name: "control prefix", target: "\x01/account", safe: false},
		{name: "javascript", target: "javascript:alert(1)", safe: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := IsSafeRedirect(
				request,
				test.target,
				[]string{"admin.example.com"},
				true,
			)
			if got != test.safe {
				t.Fatalf("IsSafeRedirect(%q) = %v, want %v", test.target, got, test.safe)
			}
		})
	}
}

func TestMiddlewareOrderIsExplicit(t *testing.T) {
	var events []string
	record := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				events = append(events, name+" before")
				next.ServeHTTP(response, request)
				events = append(events, name+" after")
			})
		}
	}
	handler := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			events = append(events, "handler")
		}),
		record("outer"),
		record("inner"),
	)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	want := "outer before,inner before,handler,inner after,outer after"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestRecoveryReturnsStructuredErrorWithoutLeakingPanic(t *testing.T) {
	handler := Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("database password")
		}),
		RequestID(),
		Recover(),
		SecurityHeaders(SecurityHeadersConfig{HTTPS: true}),
	)
	request := httptest.NewRequest(http.MethodGet, "https://example.com/panic", nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("body = %q", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "database password") {
		t.Fatalf("panic leaked in body %q", response.Body.String())
	}
	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" || !strings.Contains(response.Body.String(), requestID) {
		t.Fatalf("request ID header=%q body=%q", requestID, response.Body.String())
	}
	for name, value := range map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"Referrer-Policy":         "same-origin",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
	} {
		if got := response.Header().Get(name); !strings.Contains(got, value) {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestBodyLimitRejectsDeclaredAndStreamingOversizeBodies(t *testing.T) {
	handler := BodyLimit(5)(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, err := io.ReadAll(request.Body)
		switch {
		case errors.As(err, new(*http.MaxBytesError)):
			WriteError(response, request, http.StatusRequestEntityTooLarge, "body_too_large")
		case err != nil:
			WriteError(response, request, http.StatusBadRequest, "invalid_body")
		default:
			response.WriteHeader(http.StatusNoContent)
		}
	}))

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/", strings.NewReader("123456")),
		httptest.NewRequest(http.MethodPost, "/", struct{ io.Reader }{strings.NewReader("123456")}),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413; body=%q", response.Code, response.Body.String())
		}
	}
}

func TestTrustedProxyOnlyHonorsForwardingFromDeclaredNetworks(t *testing.T) {
	handler := TrustedProxy([]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
	})(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, RemoteIP(request).String())
	}))

	for _, test := range []struct {
		remote string
		header string
		want   string
	}{
		{remote: "10.1.2.3:1234", header: "203.0.113.9, 10.1.2.3", want: "203.0.113.9"},
		{remote: "10.1.2.3:1234", header: "198.51.100.8, 203.0.113.9", want: "203.0.113.9"},
		{remote: "192.0.2.4:1234", header: "203.0.113.9", want: "192.0.2.4"},
		{remote: "10.1.2.3:1234", header: "not-an-ip", want: "10.1.2.3"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = test.remote
		request.Header.Set("X-Forwarded-For", test.header)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if got := response.Body.String(); got != test.want {
			t.Errorf("remote=%s forwarded=%q got %q, want %q", test.remote, test.header, got, test.want)
		}
	}
}

func TestRequestIDPreservesValidUpstreamIDAndRejectsInjection(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, RequestIDFromContext(request.Context()))
	}))
	for _, test := range []struct {
		in      string
		wantIn  bool
		wantLen int
	}{
		{in: "edge-123_ABC", wantIn: true},
		{in: "invalid value with spaces", wantIn: false, wantLen: 32},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("X-Request-ID", test.in)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		got := response.Body.String()
		if test.wantIn && got != test.in {
			t.Fatalf("request ID = %q, want %q", got, test.in)
		}
		if !test.wantIn && (got == test.in || len(got) != test.wantLen) {
			t.Fatalf("generated request ID = %q", got)
		}
	}
}

func TestCancellationRemainsVisibleThroughMiddleware(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	handler := Chain(
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			if !errors.Is(request.Context().Err(), context.Canceled) {
				t.Errorf("context error = %v", request.Context().Err())
			}
		}),
		RequestID(),
		Recover(),
	)
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

// An application that reports on third-party APIs has to call them from the
// page to tell a visitor what happens from their own address. ConnectSources
// widens exactly that one directive and leaves the rest of the policy alone.
func TestSecurityHeadersWidenOnlyConnectSource(t *testing.T) {
	handler := Chain(
		http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		}),
		SecurityHeaders(SecurityHeadersConfig{
			ConnectSources: []string{"https://api.llm7.io", "https://text.pollinations.ai"},
		}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://example.com/", nil))

	policy := response.Header().Get("Content-Security-Policy")
	want := "default-src 'self'; connect-src 'self' https://api.llm7.io https://text.pollinations.ai"
	if policy != want {
		t.Fatalf("policy = %q, want %q", policy, want)
	}
	// 'self' has to be restated: connect-src replaces default-src for fetches
	// rather than adding to it, so dropping it would cost the application its
	// own API calls.
	if !strings.Contains(policy, "connect-src 'self'") {
		t.Fatal("connect-src must keep 'self'")
	}
}

func TestSecurityHeadersKeepDefaultPolicyWithoutConnectSources(t *testing.T) {
	handler := SecurityHeaders(SecurityHeadersConfig{})(
		http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://example.com/", nil))

	if policy := response.Header().Get("Content-Security-Policy"); policy != "default-src 'self'" {
		t.Fatalf("policy = %q, want the unwidened default", policy)
	}
}

// Each of these would produce a policy that does not mean what the caller
// wrote: a path is ignored by the browser, a delimiter rewrites the neighbouring
// directives, and a wildcard host is a wider grant than anyone typing one origin
// intended. Failing at construction puts the error where the mistake is.
func TestSecurityHeadersRejectMalformedConnectSources(t *testing.T) {
	for _, source := range []string{
		"https://api.llm7.io/v1/chat",
		"api.llm7.io",
		"https://*.llm7.io",
		"https://api.llm7.io; script-src *",
		"ftp://api.llm7.io",
		"",
	} {
		t.Run(source, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered == nil {
					t.Fatalf("connect-src source %q was accepted", source)
				}
			}()
			SecurityHeaders(SecurityHeadersConfig{ConnectSources: []string{source}})
		})
	}
}

// A caller who replaced the whole policy owns every directive in it. Merging
// ConnectSources into their string would mean the header no longer matches what
// either side wrote, so the combination is refused instead.
func TestSecurityHeadersRefuseConnectSourcesBesideAWholePolicy(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("a whole-policy override alongside ConnectSources was accepted")
		}
	}()
	SecurityHeaders(SecurityHeadersConfig{
		ContentSecurityPolicy: "default-src 'none'",
		ConnectSources:        []string{"https://api.llm7.io"},
	})
}
