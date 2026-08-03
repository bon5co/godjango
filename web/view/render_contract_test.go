package view

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestRenderNegotiatesFullPageAndHTMXFragmentFromSharedComponent(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section id="shared-content">Shared</section>`)
		return err
	})
	for _, test := range []struct {
		name       string
		htmx       bool
		full       bool
		pushHeader string
	}{
		{name: "full page", full: true},
		{name: "HTMX fragment", htmx: true, pushHeader: "/books?page=2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/books?page=2", nil)
			if test.htmx {
				request.Header.Set("HX-Request", "true")
			}
			response := httptest.NewRecorder()
			err := Render(response, request, RenderOptions{
				Title:       "Books",
				Content:     content,
				CSRFToken:   "masked-token",
				PushURL:     "/books?page=2",
				CachePolicy: NoStore,
			})
			if err != nil {
				t.Fatal(err)
			}
			body := response.Body.String()
			if !strings.Contains(body, `id="shared-content"`) {
				t.Fatalf("body = %q", body)
			}
			hasDocument := strings.Contains(body, "<!doctype html>")
			if hasDocument != test.full {
				t.Fatalf("document=%v, want %v; body=%q", hasDocument, test.full, body)
			}
			if test.full {
				for _, fragment := range []string{
					`<meta name="csrf-token" content="masked-token">`,
					`/static/godjango/htmx-2.0.9.min.js`,
					`/static/godjango/alpine-3.15.12.min.js`,
					`/static/godjango/godjango.js`,
				} {
					if !strings.Contains(body, fragment) {
						t.Errorf("full body missing %q", fragment)
					}
				}
			}
			if got := response.Header().Get("Vary"); !strings.Contains(got, "HX-Request") {
				t.Fatalf("Vary = %q", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q", got)
			}
			if got := response.Header().Get("HX-Push-Url"); got != test.pushHeader {
				t.Fatalf("HX-Push-Url = %q, want %q", got, test.pushHeader)
			}
		})
	}
}

func TestFormComponentRendersAccessibleValidationSwapAndFocus(t *testing.T) {
	component := Form(FormData{
		Action:       "/accounts/login/",
		Method:       http.MethodPost,
		CSRFToken:    "masked-token",
		ErrorSummary: []string{"Credentials were not accepted."},
		Fields: []Field{
			{
				Name:         "username",
				Label:        "Username",
				Value:        `<unsafe & value>`,
				Autocomplete: "username",
				Errors:       []string{"This username was not accepted."},
			},
			{
				Name:         "password",
				Label:        "Password",
				Type:         "password",
				Autocomplete: "current-password",
			},
		},
		SubmitLabel: "Sign in",
	})
	var output strings.Builder
	if err := component.Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, fragment := range []string{
		`method="post"`,
		`name="csrf_token" value="masked-token"`,
		`role="alert"`,
		`aria-invalid="true"`,
		`aria-describedby="username-error"`,
		`id="username-error"`,
		`autofocus`,
		`autocomplete="username"`,
		`autocomplete="current-password"`,
		`value="&lt;unsafe &amp; value&gt;"`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("form missing %q: %s", fragment, body)
		}
	}
	if strings.Contains(body, `value="<unsafe`) {
		t.Fatalf("unsafe value rendered: %s", body)
	}
}

func TestFlashAndPaginationComponentsAreAccessible(t *testing.T) {
	component := Stack(
		FlashMessages([]Flash{
			{Level: FlashSuccess, Message: "Saved."},
			{Level: FlashError, Message: "Could not save."},
		}),
		Pagination(PaginationData{
			Current: 2,
			Total:   4,
			URL: func(page int) string {
				return "/books?page=" + string(rune('0'+page))
			},
		}),
	)
	var output strings.Builder
	if err := component.Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	for _, fragment := range []string{
		`role="status"`,
		`role="alert"`,
		`aria-label="Pagination"`,
		`aria-current="page"`,
		`hx-boost="true"`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("component output missing %q: %s", fragment, body)
		}
	}
}

func TestHTMXDetectionRequiresExactTrue(t *testing.T) {
	for header, want := range map[string]bool{
		"":      false,
		"false": false,
		"TRUE":  true,
		"true":  true,
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("HX-Request", header)
		if got := IsHTMX(request); got != want {
			t.Errorf("HX-Request=%q got %v, want %v", header, got, want)
		}
	}
}

// Applications cannot reach into the document head, and the default
// Content-Security-Policy is default-src 'self', so an inline <style> inside an
// application component is dropped by the browser without any error. Linking a
// same-origin stylesheet is the supported route, and it must survive into the
// full-page response while staying out of an HTMX fragment.
func TestRenderLinksApplicationStylesheetsIntoTheHead(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	options := RenderOptions{
		Title:       "Shelf",
		Content:     content,
		Stylesheets: []string{"/static/stillworks/app.css"},
	}

	full := httptest.NewRecorder()
	if err := Render(full, httptest.NewRequest(http.MethodGet, "/llm/", nil), options); err != nil {
		t.Fatalf("render full page: %v", err)
	}
	body := full.Body.String()
	if !strings.Contains(body, `<link rel="stylesheet" href="/static/stillworks/app.css">`) {
		t.Fatalf("application stylesheet missing from head: %s", body)
	}
	if strings.Index(body, "/static/stillworks/app.css") > strings.Index(body, "<body") {
		t.Fatal("application stylesheet must be linked in the head, not the body")
	}

	fragment := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/llm/", nil)
	request.Header.Set("HX-Request", "true")
	if err := Render(fragment, request, options); err != nil {
		t.Fatalf("render fragment: %v", err)
	}
	if strings.Contains(fragment.Body.String(), "app.css") {
		t.Fatal("an HTMX fragment has no head and must not carry stylesheet links")
	}
}
