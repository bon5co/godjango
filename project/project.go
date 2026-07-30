// Package project defines compiled settings and explicit application registration.
package project

import (
	"context"
	"errors"
)

var ErrNotImplemented = errors.New("godjango project: not implemented")

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
type Project struct{}

func New(settings Settings, apps ...App) (*Project, error) {
	return nil, ErrNotImplemented
}

func (p *Project) Settings() Settings {
	return nil
}

func (p *Project) Apps() []App {
	return nil
}

func (p *Project) App(name string) (App, bool) {
	return nil, false
}

func (p *Project) Check(ctx context.Context) error {
	return ErrNotImplemented
}
