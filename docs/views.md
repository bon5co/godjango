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
