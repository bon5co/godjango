package view

import (
	"strings"

	"github.com/a-h/templ"
)

type CachePolicy string

const NoStore CachePolicy = "no-store"

type RenderOptions struct {
	Title   string
	Content templ.Component
	// Stylesheets are same-origin URLs linked into the document head, for an
	// application that ships its own CSS. They must be served by the
	// application itself: the default Content-Security-Policy is
	// default-src 'self', which permits this and forbids both inline styles and
	// third-party hosts. Ignored on an HTMX fragment response, which has no head.
	Stylesheets []string
	// Scripts are same-origin URLs loaded with defer after the framework's own
	// scripts, for an application that ships its own JavaScript. The same policy
	// applies as to Stylesheets: an inline <script> in an application component
	// is dropped by the browser, and a third-party host is refused. Ignored on an
	// HTMX fragment response, which has no head.
	Scripts []string
	// Meta is the rest of the document head: the description a search engine
	// quotes, the icon a tab shows, and the Open Graph and Twitter card tags a
	// link preview reads. Declared as data rather than as markup, because the
	// framework escapes every value on the way out and an application-supplied
	// HTML string would be a hole straight through that. Ignored on an HTMX
	// fragment response, which has no head.
	Meta        Meta
	CSRFToken   string
	PushURL     string
	CachePolicy CachePolicy
	Status      int
}

// Meta is one page's head metadata. Every field is optional and an unset field
// emits no tag at all: an empty content="" is worse than silence, because a
// scraper that finds the tag stops looking for a fallback.
type Meta struct {
	// Description is <meta name="description">, and the default text for both
	// social descriptions. One or two sentences; search engines truncate around
	// 155 characters and link previews around 200.
	Description string

	// Canonical is the address this page should be indexed under, emitted as
	// <link rel="canonical"> and, unless Social.URL says otherwise, as og:url.
	// It may be a path -- Render makes it absolute, because a relative canonical
	// is ignored by some crawlers and misread by others.
	//
	// Give the page's own stable address, not the request's: the point of the
	// tag is to collapse ?utm_source=... and other duplicate spellings onto one
	// URL. Naming a page other than this one delists this one.
	Canonical string

	// Icon is the favicon and its friends.
	Icon Icon

	// Social is the link preview: Open Graph plus the Twitter card.
	Social Social

	// Origin is the scheme and host that relative values in this struct are
	// resolved against, for example "https://stillworks.supercapybara.com". It
	// must be exactly scheme://host[:port] -- a path, a query or a trailing
	// slash is refused by Render rather than concatenated into a URL that does
	// not resolve.
	//
	// Leave it empty and Render uses the request's own origin: the client's
	// scheme as web.TrustedProxy resolved it, plus the Host header. That is
	// correct for a deployment reached under one name, and wrong in two ways
	// worth knowing about. Without TrustedProxy in the chain, a TLS-terminating
	// proxy leaves this process seeing plaintext, so every absolute URL in the
	// head claims http on an https site. And the Host header is the client's
	// text: a request that arrives under any other name that routes here has
	// that name written into the page's own canonical URL, which is the classic
	// way to hand an attacker your search ranking. A deployment that knows its
	// public origin should say so here and stop depending on either.
	Origin string
}

// Icon is the document's icon set. The hrefs may be relative and are left as
// given: a browser resolves them against the document, and unlike a social card
// they are never fetched by an off-site scraper.
type Icon struct {
	// Href is the icon URL, emitted as <link rel="icon">. A content-hashed path
	// is the point of naming it here rather than relying on /favicon.ico: the
	// hash lets the file be cached forever and still change.
	Href string

	// Type is the icon's MIME type, for example "image/svg+xml" or "image/png".
	// Omitted when empty, which leaves the browser to sniff the file.
	Type string

	// AppleTouch is the home-screen icon, emitted as
	// <link rel="apple-touch-icon">. iOS ignores rel="icon" entirely and, with
	// nothing declared, screenshots the page instead. 180x180 PNG.
	AppleTouch string
}

// Social is the link preview a chat client, a social network or a search engine
// builds from the page. Open Graph and the Twitter card are emitted as separate
// tag sets from these shared values: most scrapers fall back from twitter:* to
// og:*, but not all of them do, and the ones that do not are the ones that
// render a blank rectangle.
//
// The tags appear only on a page that declared something here or a
// Meta.Description. The defaults never trigger them by themselves, so the login
// form and the 404 do not each grow an og:title.
type Social struct {
	// Title defaults to RenderOptions.Title.
	Title string

	// Description defaults to Meta.Description.
	Description string

	// Image is the card image, emitted as og:image and twitter:image. It may be
	// a path and is made absolute by Render.
	//
	// A relative og:image is the failure this whole struct exists to prevent: it
	// is not an error anywhere, the page still serves, the tag is present in the
	// HTML, and every scraper silently declines to fetch it. Roughly 1200x630
	// for a large card, under 5MB, PNG or JPEG. Scrapers do not execute
	// JavaScript and most will not follow a redirect, so serve the file
	// directly.
	Image string

	// ImageAlt describes the image for a screen reader, emitted as og:image:alt
	// and twitter:image:alt. Omitted when empty.
	ImageAlt string

	// URL is the canonical address of the shared page, emitted as og:url. It
	// defaults to Meta.Canonical and, like it, is made absolute by Render.
	URL string

	// Type is og:type. Defaults to "website"; "article" for a post.
	Type string

	// SiteName is og:site_name, the name of the site as a whole rather than of
	// this page.
	SiteName string

	// TwitterCard is twitter:card. Defaults to "summary_large_image" when an
	// image is present and "summary" when none is, which is the pair of choices
	// that make a card render the way the image implies.
	TwitterCard string
}

// MetaTag is one resolved <meta> element. Open Graph is defined in RDFa and
// keys on property=; the Twitter card and the search description key on name=.
// The distinction is not decorative: og:image under name= is ignored by
// Facebook, and twitter:card under property= by some Twitter clients.
type MetaTag struct {
	Name     string
	Property string
	Content  string
}

// LinkTag is one resolved <link> element in the head, for icons and the
// canonical URL. Type is omitted from the output when empty.
type LinkTag struct {
	Rel  string
	Href string
	Type string
}

// Document is everything Layout needs to write the head. It is a struct rather
// than a growing parameter list because the head has now grown three times in
// quick succession -- stylesheets, then scripts, then this -- and each growth
// was a breaking change to a positional signature for callers who render the
// layout themselves.
//
// The metadata arrives here already resolved: defaults applied, relative URLs
// made absolute, empty values dropped. Layout writes tags and makes no choices,
// so what a page advertises is decided in Go, where it can be tested without
// rendering HTML.
type Document struct {
	Title       string
	CSRFToken   string
	Stylesheets []string
	Scripts     []string
	MetaTags    []MetaTag
	Links       []LinkTag
}

type FormData struct {
	Action       string
	Method       string
	CSRFToken    string
	ErrorSummary []string
	Fields       []Field
	SubmitLabel  string
}

type Field struct {
	Name         string
	Label        string
	Type         string
	Value        string
	Autocomplete string
	Errors       []string
}

func (field Field) inputType() string {
	if field.Type == "" {
		return "text"
	}
	return field.Type
}

func (data FormData) method() string {
	method := strings.ToLower(data.Method)
	if method == "" {
		return "post"
	}
	return method
}

type FlashLevel string

const (
	FlashSuccess FlashLevel = "success"
	FlashError   FlashLevel = "error"
)

type Flash struct {
	Level   FlashLevel
	Message string
}

type PaginationData struct {
	Current int
	Total   int
	URL     func(page int) string
}

func pages(total int) []int {
	result := make([]int, 0, total)
	for page := 1; page <= total; page++ {
		result = append(result, page)
	}
	return result
}
