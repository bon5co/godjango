package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/project"
	"github.com/go-chi/chi/v5"
)

type runtimeServicesApp struct {
	received RuntimeServices
}

func (*runtimeServicesApp) Name() string { return "runtime_services" }

func (app *runtimeServicesApp) RoutesWithServices(
	router chi.Router,
	services RuntimeServices,
) {
	app.received = services
	router.Get("/runtime-services", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
}

func TestRouterInjectsExplicitRuntimeServicesIntoApps(t *testing.T) {
	app := new(runtimeServicesApp)
	configured, err := project.New(validSettings{}, app)
	if err != nil {
		t.Fatal(err)
	}
	users := auth.NewManager(nil, nil)
	router, err := NewRouter(RouterConfig{
		Project:  configured,
		Services: RuntimeServices{Users: users},
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.received.Users != users {
		t.Fatal("runtime services were not passed to app route registration")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/runtime-services", nil),
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
}
