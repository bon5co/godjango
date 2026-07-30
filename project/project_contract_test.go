package project_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bon5co/godjango/project"
)

type fixtureSettings struct {
	err error
}

func (s fixtureSettings) Validate() error {
	return s.err
}

type fixtureApp struct {
	name   string
	checks []project.Check
}

func (a fixtureApp) Name() string {
	return a.name
}

func (a fixtureApp) Checks() []project.Check {
	return a.checks
}

func TestProjectPreservesExplicitAppOrder(t *testing.T) {
	settings := fixtureSettings{}
	first := fixtureApp{name: "books"}
	second := fixtureApp{name: "accounts"}

	configured, err := project.New(settings, first, second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	apps := configured.Apps()
	if len(apps) != 2 || apps[0].Name() != "books" || apps[1].Name() != "accounts" {
		t.Fatalf("Apps() order = %v", appNames(apps))
	}
	if configured.Settings() != settings {
		t.Error("Settings() did not return project settings")
	}
}

func TestAppsReturnsCopyAndLookupUsesStableName(t *testing.T) {
	configured, err := project.New(
		fixtureSettings{},
		fixtureApp{name: "books"},
		fixtureApp{name: "accounts"},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	apps := configured.Apps()
	apps[0] = fixtureApp{name: "mutated"}
	if got := configured.Apps()[0].Name(); got != "books" {
		t.Fatalf("Apps() exposed registry mutation: %q", got)
	}
	app, ok := configured.App("accounts")
	if !ok || app.Name() != "accounts" {
		t.Fatalf("App(accounts) = %v, %t", app, ok)
	}
	if _, ok := configured.App("missing"); ok {
		t.Error("App(missing) found an app")
	}
}

func TestProjectAllowsZeroApps(t *testing.T) {
	configured, err := project.New(fixtureSettings{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if len(configured.Apps()) != 0 {
		t.Fatalf("Apps() = %v, want empty", configured.Apps())
	}
	if err := configured.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestInvalidSettingsFailBeforeRegistryConstruction(t *testing.T) {
	settingsErr := errors.New("secret key is missing")
	_, err := project.New(
		fixtureSettings{err: settingsErr},
		fixtureApp{name: "books"},
	)
	if !errors.Is(err, settingsErr) {
		t.Fatalf("New() error = %v, want settings error", err)
	}
}

func TestDuplicateAndInvalidAppNamesAreRejected(t *testing.T) {
	tests := []struct {
		name string
		apps []project.App
		want string
	}{
		{
			name: "duplicate",
			apps: []project.App{
				fixtureApp{name: "books"},
				fixtureApp{name: "books"},
			},
			want: "books",
		},
		{
			name: "empty",
			apps: []project.App{
				fixtureApp{name: ""},
			},
			want: "name",
		},
		{
			name: "unstable punctuation",
			apps: []project.App{
				fixtureApp{name: "book-store"},
			},
			want: "book-store",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := project.New(fixtureSettings{}, test.apps...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want mention of %q", err, test.want)
			}
		})
	}
}

func TestAppChecksRunInOrderAndAggregateFailures(t *testing.T) {
	var order []string
	configured, err := project.New(
		fixtureSettings{},
		fixtureApp{
			name: "books",
			checks: []project.Check{
				{
					Name: "models",
					Run: func(context.Context) error {
						order = append(order, "books.models")
						return errors.New("invalid model")
					},
				},
				{
					Name: "routes",
					Run: func(context.Context) error {
						order = append(order, "books.routes")
						return errors.New("duplicate route")
					},
				},
			},
		},
		fixtureApp{
			name: "accounts",
			checks: []project.Check{{
				Name: "permissions",
				Run: func(context.Context) error {
					order = append(order, "accounts.permissions")
					return nil
				},
			}},
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = configured.Check(context.Background())
	if err == nil {
		t.Fatal("Check() error = nil")
	}
	if got := strings.Join(order, ","); got != "books.models,books.routes,accounts.permissions" {
		t.Fatalf("check order = %q", got)
	}
	for _, text := range []string{
		"books.models",
		"invalid model",
		"books.routes",
		"duplicate route",
	} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("Check() error %q missing %q", err, text)
		}
	}
}

func TestAppCheckHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false

	configured, err := project.New(
		fixtureSettings{},
		fixtureApp{
			name: "books",
			checks: []project.Check{{
				Name: "models",
				Run: func(context.Context) error {
					ran = true
					return nil
				},
			}},
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := configured.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Check() error = %v, want context.Canceled", err)
	}
	if ran {
		t.Error("check ran after context cancellation")
	}
}

func appNames(apps []project.App) []string {
	names := make([]string, len(apps))
	for index, app := range apps {
		names[index] = app.Name()
	}
	return names
}
