package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bon5co/godjango/web"
)

func TestStatelessPathsExemptSkipsMiddlewareForDeclaredPrefixes(t *testing.T) {
	paths := web.StatelessPaths{"/api"}
	applied := 0
	counting := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			applied++
			next.ServeHTTP(response, request)
		})
	}
	handler := paths.Exempt(counting)(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		},
	))

	for _, path := range []string{"/api", "/api/ping", "/api/v1/deep"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want %d", path, recorder.Code, http.StatusNoContent)
		}
	}
	if applied != 0 {
		t.Fatalf("middleware applied %d times to stateless paths, want 0", applied)
	}

	for _, path := range []string{"/", "/apiary", "/admin/api"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	}
	if applied != 3 {
		t.Fatalf("middleware applied %d times to stateful paths, want 3", applied)
	}
}

func TestStatelessPathsMarkExemptRequests(t *testing.T) {
	paths := web.StatelessPaths{"/api"}
	var stateless, stateful bool
	handler := paths.Exempt(func(next http.Handler) http.Handler { return next })(
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/api/ping" {
				stateless = web.IsStateless(request)
				return
			}
			stateful = web.IsStateless(request)
		}),
	)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if !stateless {
		t.Fatal("IsStateless = false on an exempt request, want true")
	}
	if stateful {
		t.Fatal("IsStateless = true on a stateful request, want false")
	}
}

func TestStatelessPathsResolveTraversalBeforeMatching(t *testing.T) {
	paths := web.StatelessPaths{"/api"}
	applied := 0
	counting := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			applied++
			next.ServeHTTP(response, request)
		})
	}
	handler := paths.Exempt(counting)(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {},
	))

	request := httptest.NewRequest(http.MethodGet, "/api/../admin", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if applied != 1 {
		t.Fatalf("middleware applied %d times to a traversal into a stateful path, want 1", applied)
	}
}

func TestStatelessPathsValidateRejectsUnusablePrefixes(t *testing.T) {
	for name, paths := range map[string]web.StatelessPaths{
		"empty":    {""},
		"relative": {"api"},
		"root":     {"/"},
	} {
		if err := paths.Validate(); err == nil {
			t.Fatalf("%s: Validate() = nil, want an error", name)
		}
	}
	if err := (web.StatelessPaths{"/api", "/healthz/"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestStatelessPathsExemptToleratesNilMiddleware(t *testing.T) {
	handler := web.StatelessPaths{"/api"}.Exempt(nil)(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.WriteHeader(http.StatusTeapot)
		},
	))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTeapot)
	}
}
