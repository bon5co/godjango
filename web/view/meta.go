package view

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bon5co/godjango/internal/requestscheme"
)

// resolveDocument turns the declarative options into the exact tags Layout
// writes: defaults applied, relative URLs made absolute, empty values dropped.
// It runs before Render writes anything, so a URL that cannot be resolved is a
// returned error and a 500 rather than a page that serves with a broken tag.
func resolveDocument(request *http.Request, options RenderOptions) (Document, error) {
	document := Document{
		Title:       options.Title,
		CSRFToken:   options.CSRFToken,
		Stylesheets: options.Stylesheets,
		Scripts:     options.Scripts,
	}
	origin := originResolver(request, options.Meta.Origin)

	canonical, err := absoluteURL("Meta.Canonical", options.Meta.Canonical, origin)
	if err != nil {
		return Document{}, err
	}
	socialURL, err := absoluteURL("Meta.Social.URL", options.Meta.Social.URL, origin)
	if err != nil {
		return Document{}, err
	}
	image, err := absoluteURL("Meta.Social.Image", options.Meta.Social.Image, origin)
	if err != nil {
		return Document{}, err
	}

	document.MetaTags = metaTags(options, canonical, socialURL, image)
	document.Links = linkTags(options.Meta.Icon, canonical)
	return document, nil
}

func metaTags(options RenderOptions, canonical string, socialURL string, image string) []MetaTag {
	social := options.Meta.Social
	description := strings.TrimSpace(options.Meta.Description)

	tags := make([]MetaTag, 0, 12)
	if description != "" {
		tags = append(tags, MetaTag{Name: "description", Content: description})
	}

	// The social values default to the page's own: a caller that sets a
	// description and an image gets a complete card without restating the title
	// it already gave, which is the case that would otherwise ship half-filled.
	socialTitle := firstNonEmpty(social.Title, options.Title)
	socialDescription := firstNonEmpty(social.Description, description)
	socialAddress := firstNonEmpty(socialURL, canonical)
	imageAlt := strings.TrimSpace(social.ImageAlt)

	// Whether to write a social block at all is decided from what the caller
	// declared, never from the defaults. Title alone would otherwise trigger it
	// on every page in the application, including the login form and the 404,
	// each advertising an og:title and an og:type="website" that nobody asked
	// for. A canonical URL alone does not count either: it is an indexing
	// statement, not a link preview.
	declared := firstNonEmpty(
		description,
		social.Title,
		social.Description,
		social.Image,
		social.URL,
		social.Type,
		social.SiteName,
		social.TwitterCard,
	)
	if declared == "" {
		return tags
	}

	// Order follows the Open Graph specification's four required properties
	// first. Scrapers do not care, but a human reading view-source does.
	tags = appendTag(tags, MetaTag{Property: "og:title", Content: socialTitle})
	tags = appendTag(tags, MetaTag{
		Property: "og:type",
		Content:  firstNonEmpty(social.Type, "website"),
	})
	tags = appendTag(tags, MetaTag{Property: "og:url", Content: socialAddress})
	tags = appendTag(tags, MetaTag{Property: "og:image", Content: image})
	if image != "" {
		tags = appendTag(tags, MetaTag{Property: "og:image:alt", Content: imageAlt})
	}
	tags = appendTag(tags, MetaTag{Property: "og:description", Content: socialDescription})
	tags = appendTag(tags, MetaTag{Property: "og:site_name", Content: social.SiteName})

	card := strings.TrimSpace(social.TwitterCard)
	if card == "" {
		// A card declared summary with a 1200x630 image is cropped to a square
		// thumbnail, so the image decides the default rather than the caller
		// having to know the two magic strings.
		card = "summary"
		if image != "" {
			card = "summary_large_image"
		}
	}
	tags = appendTag(tags, MetaTag{Name: "twitter:card", Content: card})
	tags = appendTag(tags, MetaTag{Name: "twitter:title", Content: socialTitle})
	tags = appendTag(tags, MetaTag{Name: "twitter:description", Content: socialDescription})
	tags = appendTag(tags, MetaTag{Name: "twitter:image", Content: image})
	if image != "" {
		tags = appendTag(tags, MetaTag{Name: "twitter:image:alt", Content: imageAlt})
	}
	return tags
}

func linkTags(icon Icon, canonical string) []LinkTag {
	links := make([]LinkTag, 0, 3)
	links = appendLink(links, LinkTag{Rel: "canonical", Href: canonical})
	links = appendLink(links, LinkTag{
		Rel:  "icon",
		Href: strings.TrimSpace(icon.Href),
		Type: strings.TrimSpace(icon.Type),
	})
	links = appendLink(links, LinkTag{
		Rel:  "apple-touch-icon",
		Href: strings.TrimSpace(icon.AppleTouch),
	})
	return links
}

// appendTag drops a tag with no content. An empty content="" attribute is not
// the same as no tag: a scraper that finds og:description empty shows nothing
// rather than falling back to the page description.
func appendTag(tags []MetaTag, tag MetaTag) []MetaTag {
	if strings.TrimSpace(tag.Content) == "" {
		return tags
	}
	return append(tags, tag)
}

func appendLink(links []LinkTag, link LinkTag) []LinkTag {
	if link.Href == "" {
		return links
	}
	return append(links, link)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// originResolver defers deciding the origin until something actually needs it,
// and decides it once. A page with no absolute URL to write must not fail
// because the deployment never named an origin, and a page with three of them
// must not get three different answers.
func originResolver(request *http.Request, declared string) func() (string, error) {
	var (
		resolved string
		failure  error
		done     bool
	)
	return func() (string, error) {
		if done {
			return resolved, failure
		}
		done = true
		if strings.TrimSpace(declared) != "" {
			resolved, failure = parseOrigin(strings.TrimSpace(declared), "Meta.Origin")
			return resolved, failure
		}
		if request == nil || request.Host == "" {
			failure = errors.New(
				"godjango view: cannot resolve an absolute URL: " +
					"the request carries no Host header and Meta.Origin is not set",
			)
			return "", failure
		}
		// web.TrustedProxy decided the scheme against this deployment's declared
		// trust boundary; re-deriving it from request.TLS here would report http
		// on every TLS-terminated deployment, which is to say on every real one.
		resolved, failure = parseOrigin(
			requestscheme.Of(request)+"://"+request.Host,
			"the request's Host header",
		)
		return resolved, failure
	}
}

// parseOrigin accepts scheme://host[:port] and nothing else. A trailing slash
// or a path prefix is refused rather than concatenated: joining an origin that
// already ends in a path to a rooted path produces a URL that resolves to the
// wrong place, and the resulting card is only wrong once it is shared.
func parseOrigin(candidate string, source string) (string, error) {
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", fmt.Errorf("godjango view: %s %q is not a URL: %w", source, candidate, err)
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return "", fmt.Errorf(
			"godjango view: %s %q must be an http or https origin",
			source,
			candidate,
		)
	case parsed.Host == "":
		return "", fmt.Errorf("godjango view: %s %q names no host", source, candidate)
	case parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil:
		return "", fmt.Errorf(
			"godjango view: %s %q must be scheme://host[:port] with no path, query or fragment",
			source,
			candidate,
		)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// absoluteURL returns value as an absolute http or https URL, resolving a rooted
// path against the origin.
//
// Everything it refuses, it refuses loudly. A relative og:image or canonical is
// the silent failure this exists to prevent: the page serves, the tag is in the
// HTML, and every scraper declines to fetch it with no error anywhere. So is a
// protocol-relative //host/path, which several scrapers fetch as a file path,
// and a document-relative card.png, which resolves differently on every page
// that renders the same component.
func absoluteURL(field string, value string, origin func() (string, error)) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("godjango view: %s %q is not a URL: %w", field, value, err)
	}
	if parsed.Scheme != "" {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf(
				"godjango view: %s %q must be an http or https URL",
				field,
				value,
			)
		}
		if parsed.Host == "" {
			return "", fmt.Errorf("godjango view: %s %q names no host", field, value)
		}
		return parsed.String(), nil
	}
	if parsed.Host != "" || strings.HasPrefix(trimmed, "//") {
		return "", fmt.Errorf(
			"godjango view: %s %q is protocol-relative; name the scheme",
			field,
			value,
		)
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		return "", fmt.Errorf(
			"godjango view: %s %q must be a rooted path or an absolute URL",
			field,
			value,
		)
	}
	base, err := origin()
	if err != nil {
		return "", fmt.Errorf("godjango view: %s %q cannot be made absolute: %w", field, value, err)
	}
	return base + parsed.String(), nil
}
