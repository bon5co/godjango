// Package env declares and loads typed application environment variables.
package env

import "errors"

var ErrNotImplemented = errors.New("godjango env: not implemented")

// Secret is an environment value that must be redacted in diagnostics.
type Secret string

func (Secret) String() string {
	return "[REDACTED]"
}

func (s Secret) Reveal() string {
	return string(s)
}

// Declaration binds one environment variable to a typed destination.
type Declaration struct {
	name         string
	target       any
	required     bool
	defaultValue any
}

// Required declares a variable that must exist.
func Required[T any](name string, target *T) Declaration {
	return Declaration{name: name, target: target, required: true}
}

// Optional declares a variable with an explicit default.
func Optional[T any](name string, target *T, defaultValue T) Declaration {
	return Declaration{name: name, target: target, defaultValue: defaultValue}
}

// Schema is the complete environment contract for an application.
type Schema struct {
	declarations []Declaration
}

func New(declarations ...Declaration) *Schema {
	return &Schema{declarations: declarations}
}

type loadOptions struct {
	environment      map[string]string
	workingDirectory string
	loadDotEnv       bool
}

// Option configures one Load operation.
type Option func(*loadOptions)

// WithEnvironment replaces the process environment source. It is primarily
// useful for deterministic tests and isolated launchers.
func WithEnvironment(environment map[string]string) Option {
	return func(options *loadOptions) {
		options.environment = environment
	}
}

// WithWorkingDirectory selects the directory containing the default .env file.
func WithWorkingDirectory(directory string) Option {
	return func(options *loadOptions) {
		options.workingDirectory = directory
	}
}

// WithoutDotEnv disables default .env loading.
func WithoutDotEnv() Option {
	return func(options *loadOptions) {
		options.loadDotEnv = false
	}
}

// Load validates all declarations before assigning any destination.
func (s *Schema) Load(options ...Option) error {
	return ErrNotImplemented
}
