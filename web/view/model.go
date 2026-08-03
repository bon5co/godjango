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
	CSRFToken   string
	PushURL     string
	CachePolicy CachePolicy
	Status      int
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
