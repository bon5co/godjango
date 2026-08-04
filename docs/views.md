# Views

GoDjangGo uses one server-rendered component tree for ordinary navigation and
HTMX requests:

- [templ v0.3.1020](https://github.com/a-h/templ/releases/tag/v0.3.1020)
  provides type-safe components.
- [HTMX v2.0.9](https://github.com/bigskysoftware/htmx/releases/tag/v2.0.9)
  owns requests, fragment swaps, history, and persistent server state.
- [Alpine CSP v3.15.12](https://github.com/alpinejs/alpine/tree/v3.15.12/packages/csp)
  is limited to transient disclosure, dropdown, tab, and modal state.

`view.Render` wraps a component in `view.Layout` for an ordinary request and
returns the same component as a fragment when `HX-Request: true`. It varies
caches on that header. `PushURL` becomes `HX-Push-Url` only for an HTMX
response.

```go
err := view.Render(response, request, view.RenderOptions{
    Title:       "Books",
    Content:     books.List(items),
    CSRFToken:   web.CSRFToken(request),
    PushURL:     request.URL.RequestURI(),
    CachePolicy: view.NoStore,
})
```

`view.Form` renders CSRF fields, accessible summaries and field errors, and
focuses the first invalid field after an HTMX validation swap. The local bridge
copies the masked token from the document meta element into the `X-CSRF-Token`
request header. Cookie and form-token validation still happens on the server.

`view.FlashMessages` and `view.Pagination` provide accessible defaults. Apps
can compose or replace these exported components; no template lookup or global
override registry is hidden behind them.

## Assets

`web.NewRouter` serves framework assets under `/static/godjango/`. They are
version-pinned, embedded in the Go binary, protected by content hashes, and
served with immutable one-year caching. There is no `collectstatic` step and no
runtime CDN dependency.

The Alpine artifact is the CSP build. The default `default-src 'self'` policy
therefore remains strict; applications do not need `unsafe-eval`.

Vendored SHA-256 checksums:

```text
57d9191515339922bd1356d7b2d80b1ee3b29f1b3a2c65a078bb8b2e8fd9ae5f  htmx-2.0.9.min.js
566167134bb2347110904e2ced6e816d2e8d837200c158f98b72372b3bb0b9a6  alpine-3.15.12.min.js
```

Commit generated `*_templ.go` files beside their `.templ` sources so consumers
can build projects without installing the templ generator. During development:

```bash
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate
```

## Application stylesheets

`Layout` owns the document head and the default policy is `default-src 'self'`,
so an inline `<style>` inside an application component is dropped by the browser
with no error and no visible cause. Applications ship CSS by serving it
themselves and linking it:

```go
err := view.Render(response, request, view.RenderOptions{
    Title:       "Shelf",
    Content:     shelf.Page(rows),
    Stylesheets: []string{"/static/stillworks/app.css"},
})
```

The URLs must be same-origin, which is what the policy already permits; a CDN
host would need the policy widened and is not supported. `Stylesheets` is
ignored for an HTMX fragment response, which has no head to link into.

## Application scripts

`RenderOptions.Scripts` is the same contract for JavaScript. The default policy
forbids an inline `<script>`, so an application that needs behaviour on the page
serves a file and names it:

```go
err := view.Render(response, request, view.RenderOptions{
    Title:   "Shelf",
    Content: shelf.Page(rows),
    Scripts: []string{"/static/stillworks/app.js"},
})
```

Scripts load with `defer`, after the framework's own htmx, Alpine and
`godjango.js`, so an application script can rely on those being defined. Like
stylesheets they must be same-origin, and they are ignored for an HTMX fragment.

## Head metadata

`RenderOptions.Meta` is the rest of the head: the description, the icon, the
canonical URL, and the Open Graph and Twitter tags a link preview reads. It is
declarative data, not markup — every value is escaped into its attribute, and
there is deliberately no way to hand the framework a head HTML string, because
that would be a hole through the same policy the rest of the head maintains.

```go
err := view.Render(response, request, view.RenderOptions{
    Title:   "Keyless LLM registry",
    Content: registry.Page(rows),
    Meta: view.Meta{
        Description: "Every keyless LLM endpoint, probed hourly.",
        Canonical:   "/llm/",
        Icon: view.Icon{
            Href:       "/static/stillworks/icon.a1b2c3.svg",
            Type:       "image/svg+xml",
            AppleTouch: "/static/stillworks/apple-touch-icon.a1b2c3.png",
        },
        Social: view.Social{
            Image:    "/static/stillworks/card.a1b2c3.png",
            ImageAlt: "The stillworks registry",
            SiteName: "stillworks",
        },
        Origin: settings.SiteOrigin,
    },
})
```

Defaults fill the rest: `og:title` from `Title`, `og:description` from
`Description`, `og:url` from `Canonical`, `og:type` from `"website"`, and
`twitter:card` from whether an image is present — `summary_large_image` when it
is, `summary` when it is not. A field nobody set emits no tag; an empty
`content=""` would tell a scraper the page has no description rather than
letting it fall back. The social tags appear only on a page that declared a
description or something under `Social`, so the login form does not grow an
`og:title`. Like stylesheets and scripts, `Meta` is ignored for an HTMX
fragment, which has no head.

### Absolute URLs

`og:image` and the canonical URL must be absolute. A relative one is not an
error anywhere: the page serves, the tag is in the HTML, and every scraper
declines to fetch it, which is a broken link preview nobody is told about.

`Canonical`, `Social.URL` and `Social.Image` may therefore be given as rooted
paths and are resolved against an origin before they are written. The origin
comes from `Meta.Origin` when the deployment names one, and otherwise from the
request: the client's scheme as `web.TrustedProxy` resolved it, plus the `Host`
header.

Name `Meta.Origin` in production. Falling back to the request means a `Host`
header the client typed decides this page's canonical URL, and it means that
without `web.TrustedProxy` in the middleware chain a TLS-terminating proxy
leaves every URL in the head claiming `http`.

Everything that cannot be resolved fails the render before a byte is written —
a protocol-relative `//host/path`, a document-relative `card.png`, a
`data:` URL, an origin with a path or no scheme, a relative path with no origin
available. The handler serves a 500 instead of a page whose previews never
worked.

## Calling a third-party origin from the page

`default-src 'self'` also covers `connect-src`, so `fetch()` to any other host is
blocked. An application that genuinely has to call an upstream API *from the
visitor's browser* — because the answer depends on the visitor's own address, not
the server's — names those origins on the security middleware:

```go
web.SecurityHeaders(web.SecurityHeadersConfig{
    HTTPS:          !settings.Debug,
    ConnectSources: []string{"https://api.llm7.io", "https://text.pollinations.ai"},
})
```

The resulting policy is `default-src 'self'; connect-src 'self' <origins>`.
Nothing else widens: scripts, styles, images and frames stay same-origin. Each
entry must be a bare `scheme://host[:port]`; a path, a wildcard or a stray
delimiter is refused at construction rather than serving a policy that does not
mean what was written. `ContentSecurityPolicy` still replaces the whole policy,
and cannot be combined with `ConnectSources`.
