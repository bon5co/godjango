// Package project defines compiled settings and explicit application registration.
package project

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
)

var (
	ErrInvalidSettings = errors.New("godjango project: invalid settings")
	ErrInvalidApp      = errors.New("godjango project: invalid app")
	ErrCheckFailed     = errors.New("godjango project: check failed")
)

var appNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Settings is the project-owned configuration contract.
type Settings interface {
	Validate() error
}

// App is the stable identity shared by every project capability.
type App interface {
	Name() string
}

// Check validates one app-owned invariant.
type Check struct {
	Name string
	Run  func(context.Context) error
}

// CheckProvider is implemented by apps with startup diagnostics.
type CheckProvider interface {
	Checks() []Check
}

// Project is the validated settings and ordered app registry.
type Project struct {
	settings Settings
	apps     []App
	appsByID map[string]App
}

func New(settings Settings, apps ...App) (*Project, error) {
	if isNil(settings) {
		return nil, fmt.Errorf("%w: settings are required", ErrInvalidSettings)
	}
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSettings, err)
	}

	configured := &Project{
		settings: settings,
		apps:     make([]App, 0, len(apps)),
		appsByID: make(map[string]App, len(apps)),
	}
	for _, app := range apps {
		if isNil(app) {
			return nil, fmt.Errorf("%w: app is nil", ErrInvalidApp)
		}
		name := app.Name()
		if !appNamePattern.MatchString(name) {
			return nil, fmt.Errorf(
				"%w: app name %q must match %s",
				ErrInvalidApp,
				name,
				appNamePattern,
			)
		}
		if _, duplicate := configured.appsByID[name]; duplicate {
			return nil, fmt.Errorf("%w: app %q is declared more than once", ErrInvalidApp, name)
		}
		configured.apps = append(configured.apps, app)
		configured.appsByID[name] = app
	}
	return configured, nil
}

func (p *Project) Settings() Settings {
	return p.settings
}

func (p *Project) Apps() []App {
	return append([]App(nil), p.apps...)
}

func (p *Project) App(name string) (App, bool) {
	app, ok := p.appsByID[name]
	return app, ok
}

func (p *Project) Check(ctx context.Context) error {
	var checkErrors []error
	for _, app := range p.apps {
		provider, ok := app.(CheckProvider)
		if !ok {
			continue
		}
		for _, check := range provider.Checks() {
			if err := ctx.Err(); err != nil {
				checkErrors = append(checkErrors, err)
				return errors.Join(checkErrors...)
			}
			identity := app.Name() + "." + check.Name
			if check.Name == "" {
				checkErrors = append(
					checkErrors,
					fmt.Errorf("%w: %s has an empty check name", ErrCheckFailed, app.Name()),
				)
				continue
			}
			if check.Run == nil {
				checkErrors = append(
					checkErrors,
					fmt.Errorf("%w: %s has no runner", ErrCheckFailed, identity),
				)
				continue
			}
			if err := check.Run(ctx); err != nil {
				checkErrors = append(
					checkErrors,
					fmt.Errorf("%w: %s: %w", ErrCheckFailed, identity, err),
				)
			}
		}
	}
	return errors.Join(checkErrors...)
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
