package view

import (
	"errors"
	"net/http"
	"strings"
)

func Render(response http.ResponseWriter, request *http.Request, options RenderOptions) error {
	if options.Content == nil {
		return errors.New("godjango view: content component is required")
	}
	appendVary(response.Header(), "HX-Request")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if options.CachePolicy != "" {
		response.Header().Set("Cache-Control", string(options.CachePolicy))
	}
	component := options.Content
	if IsHTMX(request) {
		if options.PushURL != "" {
			response.Header().Set("HX-Push-Url", options.PushURL)
		}
	} else {
		component = Layout(options.Title, options.CSRFToken, options.Stylesheets, component)
	}
	if options.Status != 0 {
		response.WriteHeader(options.Status)
	}
	return component.Render(request.Context(), response)
}

func IsHTMX(request *http.Request) bool {
	return request != nil &&
		strings.EqualFold(strings.TrimSpace(request.Header.Get("HX-Request")), "true")
}

func appendVary(header http.Header, value string) {
	values := make([]string, 0, len(header.Values("Vary"))+1)
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if strings.EqualFold(item, value) {
				return
			}
			values = append(values, item)
		}
	}
	values = append(values, value)
	header.Set("Vary", strings.Join(values, ", "))
}

func firstInvalidField(fields []Field) int {
	for index, field := range fields {
		if len(field.Errors) > 0 {
			return index
		}
	}
	return -1
}

func hasFieldErrors(fields []Field) bool {
	return firstInvalidField(fields) >= 0
}
