package view

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedAssetsArePinnedLocalAndImmutable(t *testing.T) {
	handler := Assets()
	for _, test := range []struct {
		path     string
		contains string
	}{
		{path: "/static/godjango/htmx-2.0.9.min.js", contains: "htmx"},
		{path: "/static/godjango/alpine-3.15.12.min.js", contains: "Alpine"},
		{path: "/static/godjango/godjango.js", contains: "htmx:configRequest"},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", test.path, response.Code)
		}
		body, err := io.ReadAll(response.Result().Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), test.contains) {
			t.Fatalf("%s does not contain %q", test.path, test.contains)
		}
		if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Fatalf("%s Cache-Control = %q", test.path, got)
		}
		if response.Header().Get("ETag") == "" {
			t.Fatalf("%s has no ETag", test.path)
		}
		if got := response.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
			t.Fatalf("%s Content-Type = %q", test.path, got)
		}
	}
}

func TestEmbeddedAssetsSupportConditionalGETAndRejectUnknownPaths(t *testing.T) {
	handler := Assets()
	first := httptest.NewRecorder()
	handler.ServeHTTP(
		first,
		httptest.NewRequest(http.MethodGet, "/static/godjango/godjango.js", nil),
	)
	request := httptest.NewRequest(http.MethodGet, "/static/godjango/godjango.js", nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	handler.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", conditional.Code)
	}

	for _, path := range []string{
		"/static/godjango/unknown.js",
		"/static/godjango/../secret",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, response.Code)
		}
	}
}

func TestBrowserBridgePropagatesCSRFAndRestoresFocus(t *testing.T) {
	response := httptest.NewRecorder()
	Assets().ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/static/godjango/godjango.js", nil),
	)
	body := response.Body.String()
	for _, fragment := range []string{
		`meta[name="csrf-token"]`,
		`event.detail.headers["X-CSRF-Token"]`,
		`event.detail.xhr.status === 422`,
		`event.detail.shouldSwap = true`,
		`htmx:afterSwap`,
		`querySelector?.("[autofocus]")`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("bridge missing %q", fragment)
		}
	}
}
