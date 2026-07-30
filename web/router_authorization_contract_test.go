package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/project"
	"github.com/go-chi/chi/v5"
)

type routeSettings struct{}

func (routeSettings) Validate() error { return nil }

type routeApp struct {
	name       string
	path       string
	registered *[]string
}

func (app routeApp) Name() string { return app.name }

func (app routeApp) Routes(router chi.Router) {
	*app.registered = append(*app.registered, app.name)
	router.Get(app.path, func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, app.name)
	})
}

func TestRouterRegistersAppRoutesInExplicitProjectOrder(t *testing.T) {
	var registered []string
	configured, err := project.New(
		routeSettings{},
		routeApp{name: "accounts", path: "/accounts", registered: &registered},
		routeApp{name: "books", path: "/books", registered: &registered},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRouter(RouterConfig{
		Project: configured,
		Middleware: []Middleware{
			RequestID(),
			SecurityHeaders(SecurityHeadersConfig{}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(registered, []string{"accounts", "books"}) {
		t.Fatalf("registration order = %v", registered)
	}
	for path, want := range map[string]string{"/accounts": "accounts", "/books": "books"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Body.String() != want {
			t.Fatalf("%s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func TestPermissionMiddlewareDeniesAnonymousAndUnauthorizedUsers(t *testing.T) {
	permission := auth.Permission("books.change_book")
	handler := RequirePermission(permission)(http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		},
	))
	for _, test := range []struct {
		name   string
		user   *auth.User
		status int
	}{
		{name: "anonymous", status: http.StatusUnauthorized},
		{
			name: "direct",
			user: &auth.User{
				IsActive:          true,
				DirectPermissions: []auth.Permission{permission},
			},
			status: http.StatusNoContent,
		},
		{
			name: "group",
			user: &auth.User{
				IsActive: true,
				Groups: []auth.Group{{
					Name:        "editors",
					Permissions: []auth.Permission{permission},
				}},
			},
			status: http.StatusNoContent,
		},
		{
			name:   "denied",
			user:   &auth.User{IsActive: true},
			status: http.StatusForbidden,
		},
		{
			name:   "inactive",
			user:   &auth.User{IsActive: false, DirectPermissions: []auth.Permission{permission}},
			status: http.StatusForbidden,
		},
		{
			name:   "superuser",
			user:   &auth.User{IsActive: true, IsSuperuser: true},
			status: http.StatusNoContent,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.user != nil {
				ctx := context.WithValue(
					request.Context(),
					currentUserContextKey{},
					test.user,
				)
				request = request.WithContext(ctx)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}
