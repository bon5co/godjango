package web

import (
	"errors"
	"net/http"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/project"
	"github.com/bon5co/godjango/web/view"
	"github.com/go-chi/chi/v5"
)

type RouteProvider interface {
	Routes(chi.Router)
}

type RouterConfig struct {
	Project    *project.Project
	Middleware []Middleware
}

func NewRouter(config RouterConfig) (chi.Router, error) {
	if config.Project == nil {
		return nil, errors.New("godjango web: configured project is required")
	}
	router := chi.NewRouter()
	for _, middleware := range config.Middleware {
		if middleware == nil {
			return nil, errors.New("godjango web: middleware is nil")
		}
		router.Use(middleware)
	}
	router.Handle("/static/godjango/*", view.Assets())
	for _, app := range config.Project.Apps() {
		if routes, ok := app.(RouteProvider); ok {
			routes.Routes(router)
		}
	}
	return router, nil
}

func RequirePermission(permission auth.Permission) func(http.Handler) http.Handler {
	permissions := auth.ModelBackend{RegisteredPermissions: []auth.Permission{permission}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			user, authenticated := CurrentUser(request)
			if !authenticated {
				WriteError(response, request, http.StatusUnauthorized, "authentication_required")
				return
			}
			if !permissions.HasPermission(user, permission) {
				WriteError(response, request, http.StatusForbidden, "permission_denied")
				return
			}
			next.ServeHTTP(response, request)
		})
	}
}
