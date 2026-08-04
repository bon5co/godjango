package view

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/bon5co/godjango/internal/requestscheme"
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

// The same argument as for stylesheets applies to JavaScript, and one step
// further: an application that must call a third-party API from the page has no
// way to do it from an inline handler under default-src 'self'. The script has
// to arrive as a same-origin file, in the head, and stay out of a fragment.
func TestRenderLoadsApplicationScriptsFromTheHead(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	options := RenderOptions{
		Title:   "Shelf",
		Content: content,
		Scripts: []string{"/static/stillworks/app.js"},
	}

	full := httptest.NewRecorder()
	if err := Render(full, httptest.NewRequest(http.MethodGet, "/llm/", nil), options); err != nil {
		t.Fatalf("render full page: %v", err)
	}
	body := full.Body.String()
	if !strings.Contains(body, `<script defer src="/static/stillworks/app.js"></script>`) {
		t.Fatalf("application script missing from head: %s", body)
	}
	if strings.Index(body, "/static/stillworks/app.js") > strings.Index(body, "<body") {
		t.Fatal("application script must be loaded from the head, not the body")
	}
	// After the framework's own scripts: an application script that assumed htmx
	// was defined would otherwise break depending on load order.
	if strings.Index(body, "/static/stillworks/app.js") < strings.Index(body, "godjango.js") {
		t.Fatal("application scripts must follow the framework's own")
	}

	fragment := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/llm/", nil)
	request.Header.Set("HX-Request", "true")
	if err := Render(fragment, request, options); err != nil {
		t.Fatalf("render fragment: %v", err)
	}
	if strings.Contains(fragment.Body.String(), "app.js") {
		t.Fatal("an HTMX fragment has no head and must not carry script tags")
	}
}

// A page with a finished social card image and no way to advertise it is the
// gap this covers: the tags have to be complete, absolute, and in the head of a
// full-page response. The defaults matter as much as the fields -- a caller that
// names a description and an image should not have to restate its own title for
// the card to render.
func TestRenderWritesCompleteHeadMetadataFromDeclaredValues(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	request := httptest.NewRequest(http.MethodGet, "/llm/", nil)
	request.Host = "stillworks.supercapybara.com"
	// What a TLS-terminating proxy leaves behind: a plaintext connection to this
	// process and the client's real scheme carried in the request context.
	request = request.WithContext(requestscheme.With(request.Context(), requestscheme.HTTPS))

	response := httptest.NewRecorder()
	err := Render(response, request, RenderOptions{
		Title:   "Keyless LLM registry",
		Content: content,
		Meta: Meta{
			Description: "Every keyless LLM endpoint, probed hourly.",
			Canonical:   "/llm/",
			Icon: Icon{
				Href:       "/static/stillworks/icon.a1b2c3.svg",
				Type:       "image/svg+xml",
				AppleTouch: "/static/stillworks/apple-touch-icon.a1b2c3.png",
			},
			Social: Social{
				Image:    "/static/stillworks/card.a1b2c3.png",
				ImageAlt: "The stillworks registry",
				SiteName: "stillworks",
			},
		},
	})
	if err != nil {
		t.Fatalf("render full page: %v", err)
	}

	body := response.Body.String()
	head := strings.Join([]string{
		`<meta name="description" content="Every keyless LLM endpoint, probed hourly.">`,
		`<meta property="og:title" content="Keyless LLM registry">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:url" content="https://stillworks.supercapybara.com/llm/">`,
		`<meta property="og:image" ` +
			`content="https://stillworks.supercapybara.com/static/stillworks/card.a1b2c3.png">`,
		`<meta property="og:image:alt" content="The stillworks registry">`,
		`<meta property="og:description" content="Every keyless LLM endpoint, probed hourly.">`,
		`<meta property="og:site_name" content="stillworks">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:title" content="Keyless LLM registry">`,
		`<meta name="twitter:description" content="Every keyless LLM endpoint, probed hourly.">`,
		`<meta name="twitter:image" ` +
			`content="https://stillworks.supercapybara.com/static/stillworks/card.a1b2c3.png">`,
		`<meta name="twitter:image:alt" content="The stillworks registry">`,
		`<link rel="canonical" href="https://stillworks.supercapybara.com/llm/">`,
		`<link rel="icon" type="image/svg+xml" href="/static/stillworks/icon.a1b2c3.svg">`,
		`<link rel="apple-touch-icon" href="/static/stillworks/apple-touch-icon.a1b2c3.png">`,
	}, "")
	if !strings.Contains(body, head) {
		t.Fatalf("head metadata missing or out of order.\nwant: %s\ngot: %s", head, body)
	}
	if strings.Index(body, `property="og:image"`) > strings.Index(body, "<body") {
		t.Fatal("head metadata must be in the head, not the body")
	}
}

// The absent value is the case that goes wrong quietly: an empty content=""
// reads to a scraper as "this page declares it has no description", which stops
// it falling back to anything else. A field nobody set emits no tag at all.
func TestRenderOmitsMetadataNobodyDeclared(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	response := httptest.NewRecorder()
	err := Render(response, httptest.NewRequest(http.MethodGet, "/llm/", nil), RenderOptions{
		Title:   "Shelf",
		Content: content,
	})
	if err != nil {
		t.Fatalf("render full page: %v", err)
	}
	// The CSRF token's own meta element is excluded: it is written unconditionally
	// so the client bridge always finds it, and an empty one means "no session"
	// rather than "no value".
	head := strings.Replace(
		response.Body.String(),
		`<meta name="csrf-token" content="">`,
		"",
		1,
	)
	body := response.Body.String()
	if strings.Contains(head, `content=""`) {
		t.Fatalf("empty content attribute emitted: %s", head)
	}
	for _, absent := range []string{
		"og:",
		"twitter:",
		`name="description"`,
		`rel="canonical"`,
		`rel="icon"`,
		`rel="apple-touch-icon"`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("page declaring no metadata still emitted %q: %s", absent, body)
		}
	}
}

// The twitter:card value decides how the image is cropped, so it follows the
// image rather than waiting for the caller to know the two magic strings. An
// explicitly named card wins over both.
func TestRenderChoosesTwitterCardFromWhetherAnImageIsPresent(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	for _, test := range []struct {
		name   string
		social Social
		want   string
	}{
		{
			name:   "image implies a large card",
			social: Social{Image: "/static/card.png"},
			want:   `<meta name="twitter:card" content="summary_large_image">`,
		},
		{
			name:   "no image stays a summary",
			social: Social{Description: "A shelf."},
			want:   `<meta name="twitter:card" content="summary">`,
		},
		{
			name:   "an explicit card is honoured",
			social: Social{Image: "/static/card.png", TwitterCard: "summary"},
			want:   `<meta name="twitter:card" content="summary">`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			err := Render(response, httptest.NewRequest(http.MethodGet, "/", nil), RenderOptions{
				Title:   "Shelf",
				Content: content,
				Meta:    Meta{Social: test.social},
			})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("want %q in %s", test.want, response.Body.String())
			}
		})
	}
}

// Meta.Origin exists for the deployment that knows its public address, and it
// has to beat the request's Host header: a request arriving under any other name
// that routes here would otherwise write that name into this page's canonical
// URL.
func TestRenderPrefersTheDeclaredOriginOverTheRequestHost(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	request := httptest.NewRequest(http.MethodGet, "/llm/", nil)
	request.Host = "attacker.example"

	response := httptest.NewRecorder()
	err := Render(response, request, RenderOptions{
		Title:   "Shelf",
		Content: content,
		Meta: Meta{
			Canonical: "/llm/",
			Origin:    "https://stillworks.supercapybara.com",
			Social: Social{
				// Already absolute, and on another host: passed through, because
				// a card served from a CDN is a legitimate thing to declare.
				Image: "https://cdn.example.com/card.png",
			},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := response.Body.String()
	for _, want := range []string{
		`<link rel="canonical" href="https://stillworks.supercapybara.com/llm/">`,
		`<meta property="og:url" content="https://stillworks.supercapybara.com/llm/">`,
		`<meta property="og:image" content="https://cdn.example.com/card.png">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "attacker.example") {
		t.Fatalf("request Host leaked into the head over a declared origin: %s", body)
	}
}

// Every one of these produces a tag that is present, syntactically fine, and
// silently ignored by the scrapers it was written for. They fail the render
// instead, before a byte is written, so the handler serves a 500 that someone
// notices rather than a page whose previews never worked.
func TestRenderRefusesMetadataURLsItCannotMakeAbsolute(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	for _, test := range []struct {
		name string
		host string
		meta Meta
	}{
		{
			name: "protocol-relative image",
			host: "stillworks.supercapybara.com",
			meta: Meta{Social: Social{Image: "//cdn.example.com/card.png"}},
		},
		{
			name: "document-relative image",
			host: "stillworks.supercapybara.com",
			meta: Meta{Social: Social{Image: "card.png"}},
		},
		{
			name: "image on a scheme no scraper fetches",
			host: "stillworks.supercapybara.com",
			meta: Meta{Social: Social{Image: "data:image/png;base64,iVBORw0KGgo="}},
		},
		{
			name: "origin carrying a path",
			host: "stillworks.supercapybara.com",
			meta: Meta{
				Origin: "https://stillworks.supercapybara.com/",
				Social: Social{Image: "/static/card.png"},
			},
		},
		{
			name: "origin without a scheme",
			host: "stillworks.supercapybara.com",
			meta: Meta{
				Origin: "stillworks.supercapybara.com",
				Social: Social{Image: "/static/card.png"},
			},
		},
		{
			name: "no origin to resolve against",
			meta: Meta{Canonical: "/llm/"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/llm/", nil)
			request.Host = test.host
			response := httptest.NewRecorder()
			err := Render(response, request, RenderOptions{
				Title:   "Shelf",
				Content: content,
				Meta:    test.meta,
			})
			if err == nil {
				t.Fatalf("render accepted %q: %s", test.name, response.Body.String())
			}
			if response.Body.Len() != 0 {
				t.Fatalf("body written before the failure: %s", response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "" {
				t.Fatalf("headers written before the failure: Content-Type=%q", got)
			}
		})
	}
}

// Metadata is prose an application accepts from wherever it likes -- a database
// row, a form, a CMS -- and it lands in an HTML attribute. Nothing in it may
// close that attribute or open a tag.
func TestRenderEscapesMetadataIntoAttributes(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	hostile := `Quote " angle <script>alert('x')</script> & ampersand`
	request := httptest.NewRequest(http.MethodGet, "/llm/", nil)
	request.Host = "stillworks.supercapybara.com"

	response := httptest.NewRecorder()
	err := Render(response, request, RenderOptions{
		Title:   "Shelf",
		Content: content,
		Meta: Meta{
			Description: hostile,
			Social:      Social{Image: "/static/card.png", ImageAlt: hostile},
		},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := response.Body.String()
	escaped := `Quote &#34; angle &lt;script&gt;alert(&#39;x&#39;)&lt;/script&gt; &amp; ampersand`
	for _, want := range []string{
		`<meta name="description" content="` + escaped + `">`,
		`<meta property="og:image:alt" content="` + escaped + `">`,
		`<meta property="og:description" content="` + escaped + `">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing escaped tag %q: %s", want, body)
		}
	}
	head := body[:strings.Index(body, "</head>")]
	for _, forbidden := range []string{"<script>", `alert('x')`, `content="Quote "`} {
		if strings.Contains(head, forbidden) {
			t.Fatalf("unescaped %q reached the head: %s", forbidden, head)
		}
	}
}

// A fragment has no head, so the metadata has nowhere to go. It is dropped for
// the same reason stylesheets and scripts are, rather than being appended to the
// swapped markup where it would accumulate on every swap.
func TestRenderLeavesHeadMetadataOutOfAnHTMXFragment(t *testing.T) {
	content := templ.ComponentFunc(func(_ context.Context, writer io.Writer) error {
		_, err := io.WriteString(writer, `<section>Shelf</section>`)
		return err
	})
	request := httptest.NewRequest(http.MethodGet, "/llm/", nil)
	request.Host = "stillworks.supercapybara.com"
	request.Header.Set("HX-Request", "true")

	response := httptest.NewRecorder()
	err := Render(response, request, RenderOptions{
		Title:   "Shelf",
		Content: content,
		Meta: Meta{
			Description: "Every keyless LLM endpoint, probed hourly.",
			Canonical:   "/llm/",
			Icon:        Icon{Href: "/static/icon.svg"},
			Social:      Social{Image: "/static/card.png"},
		},
	})
	if err != nil {
		t.Fatalf("render fragment: %v", err)
	}
	body := response.Body.String()
	if body != `<section>Shelf</section>` {
		t.Fatalf("fragment carried head metadata: %s", body)
	}
}
