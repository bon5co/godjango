package web

import (
	"net/http"
	"sort"
	"strings"
)

// BodyLimits is a maximum request body that differs by path prefix.
//
// BodyLimit takes one number for the whole application, and for most
// applications one number is right: a form that can post twenty megabytes is a
// denial-of-service surface with no purpose, and 1 MiB is a generous form.
// What one number cannot express is an application whose HTML pages want that
// ceiling and whose upload endpoint has to accept far more -- an image API, a
// document importer, an attachment route. Raising the single limit for those
// raises it for the login form too, which is the wrong trade in the one place
// it matters.
//
// Django has the same shape of setting, DATA_UPLOAD_MAX_MEMORY_SIZE, and the
// same gap: it is global, and applications that need a larger body on one view
// end up bypassing the check entirely. This keeps the check and varies the
// number.
//
//	web.BodyLimits{
//		Default: 1 << 20,
//		ByPrefix: map[string]int64{"/api/uploads": 22 << 20},
//	}.Middleware()
//
// Prefixes match the same way StatelessPaths does -- exactly, or at a segment
// boundary -- so "/api" covers "/api/uploads" and does not cover "/apiary", and
// a path is cleaned before matching so "/api/../admin" is matched as "/admin".
// When two prefixes both match, the longer one wins, so a general "/api" rule
// can be narrowed by a specific "/api/uploads" one without ordering mattering.
type BodyLimits struct {
	// Default applies to every path no prefix claims. Zero or negative is a
	// configuration error, refused by Validate and by Middleware, because a
	// limit of zero would reject every request with a body and a negative one
	// would mean nothing at all.
	Default int64

	// ByPrefix raises or lowers the limit for a family of routes.
	ByPrefix map[string]int64

	// Reject writes the refusal. It is called with the limit that was
	// exceeded, before any of the body has been read, and it must write a
	// complete response.
	//
	// Nil is the framework's own answer: 413 with the body_too_large code, the
	// same one BodyLimit writes. An application serving somebody else's wire
	// contract supplies its own, because a caller migrating from that contract
	// parses the error body it already knows.
	Reject func(response http.ResponseWriter, request *http.Request, limit int64)
}

// Validate reports a configuration that cannot mean what it says. Servers call
// it at startup rather than discovering a zero limit as universal rejection
// under traffic.
func (limits BodyLimits) Validate() error {
	if limits.Default <= 0 {
		return errBodyLimit("default body limit must be greater than zero")
	}
	for prefix, limit := range limits.ByPrefix {
		trimmed := strings.TrimSpace(prefix)
		if trimmed == "" {
			return errBodyLimit("body limit prefix is empty")
		}
		if !strings.HasPrefix(trimmed, "/") {
			return errBodyLimit("body limit prefix must start with /: " + prefix)
		}
		if limit <= 0 {
			return errBodyLimit("body limit for " + prefix + " must be greater than zero")
		}
	}
	return nil
}

// Middleware refuses an oversized body and caps the reader for the rest.
//
// It panics on an invalid configuration rather than serving one: a limit of
// zero rejects every request with a body, which is a failure that looks like
// an application bug from every angle except the one line that caused it.
func (limits BodyLimits) Middleware() Middleware {
	if err := limits.Validate(); err != nil {
		panic(err)
	}
	// Sorted once, longest first, so the most specific prefix wins and map
	// iteration order cannot decide which limit a route gets.
	prefixes := make([]string, 0, len(limits.ByPrefix))
	for prefix := range limits.ByPrefix {
		prefixes = append(prefixes, normalizePrefix(strings.TrimSpace(prefix)))
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	byNormalized := make(map[string]int64, len(limits.ByPrefix))
	for prefix, limit := range limits.ByPrefix {
		byNormalized[normalizePrefix(strings.TrimSpace(prefix))] = limit
	}
	reject := limits.Reject
	if reject == nil {
		reject = func(response http.ResponseWriter, request *http.Request, _ int64) {
			WriteError(response, request, http.StatusRequestEntityTooLarge, "body_too_large")
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			limit := limits.Default
			path := requestPath(request)
			for _, prefix := range prefixes {
				if matchesPrefix(path, prefix) {
					limit = byNormalized[prefix]
					break
				}
			}
			// A declared length over the limit is refused without reading a
			// byte; an undeclared one is capped, so a chunked body cannot walk
			// past the limit either.
			if request.ContentLength > limit {
				reject(response, request, limit)
				return
			}
			request.Body = http.MaxBytesReader(response, request.Body, limit)
			next.ServeHTTP(response, request)
		})
	}
}

// LimitFor is the limit that applies to a path. Exported for an application
// that has to state its own ceiling -- in an error message, or in the docs
// page that tells a caller how large an upload may be -- without restating the
// configuration.
func (limits BodyLimits) LimitFor(path string) int64 {
	best := limits.Default
	bestLength := -1
	for prefix, limit := range limits.ByPrefix {
		normalized := normalizePrefix(strings.TrimSpace(prefix))
		if matchesPrefix(cleanPath(path), normalized) && len(normalized) > bestLength {
			best, bestLength = limit, len(normalized)
		}
	}
	return best
}

type bodyLimitError string

func (e bodyLimitError) Error() string { return "godjango web: " + string(e) }

func errBodyLimit(message string) error { return bodyLimitError(message) }
