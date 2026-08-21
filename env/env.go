// Package env declares and loads typed application environment variables.
package env

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

var ErrInvalidEnvironment = errors.New("godjango env: invalid environment")

// Secret is an environment value that must be redacted in diagnostics.
type Secret string

const redactedSecret = "[REDACTED]"

func (Secret) String() string {
	return redactedSecret
}

func (Secret) GoString() string {
	return "env.Secret(" + strconv.Quote(redactedSecret) + ")"
}

func (Secret) LogValue() slog.Value {
	return slog.StringValue(redactedSecret)
}

func (Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(redactedSecret)
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
		options.environment = cloneEnvironment(environment)
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
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("%w: determine working directory", ErrInvalidEnvironment)
	}
	config := loadOptions{
		environment:      processEnvironment(),
		workingDirectory: workingDirectory,
		loadDotEnv:       true,
	}
	for _, option := range options {
		option(&config)
	}

	values := make(map[string]string)
	if config.loadDotEnv {
		path := filepath.Join(config.workingDirectory, ".env")
		dotEnv, readErr := godotenv.Read(path)
		switch {
		case readErr == nil:
			for name, value := range dotEnv {
				values[name] = value
			}
		case errors.Is(readErr, os.ErrNotExist):
		default:
			return fmt.Errorf(
				"%w: %s could not be read or parsed",
				ErrInvalidEnvironment,
				path,
			)
		}
	}
	for name, value := range config.environment {
		values[name] = value
	}

	assignments, validationErrors := s.validate(values)
	if len(validationErrors) != 0 {
		return fmt.Errorf(
			"%w:\n%v",
			ErrInvalidEnvironment,
			errors.Join(validationErrors...),
		)
	}
	for _, assign := range assignments {
		assign()
	}
	return nil
}

func (s *Schema) validate(values map[string]string) ([]func(), []error) {
	assignments := make([]func(), 0, len(s.declarations))
	var validationErrors []error
	seen := make(map[string]struct{}, len(s.declarations))

	for _, declaration := range s.declarations {
		if declaration.name == "" {
			validationErrors = append(validationErrors, errors.New("environment variable name is empty"))
			continue
		}
		if _, duplicate := seen[declaration.name]; duplicate {
			validationErrors = append(
				validationErrors,
				fmt.Errorf("%s is declared more than once", declaration.name),
			)
			continue
		}
		seen[declaration.name] = struct{}{}

		raw, exists := values[declaration.name]
		if !exists {
			if declaration.required {
				validationErrors = append(
					validationErrors,
					fmt.Errorf("%s is required", declaration.name),
				)
				continue
			}
			assignment, assignmentErr := defaultAssignment(declaration)
			if assignmentErr != nil {
				validationErrors = append(validationErrors, assignmentErr)
				continue
			}
			assignments = append(assignments, assignment)
			continue
		}

		assignment, parseErr := parsedAssignment(declaration, raw)
		if parseErr != nil {
			validationErrors = append(validationErrors, parseErr)
			continue
		}
		assignments = append(assignments, assignment)
	}
	return assignments, validationErrors
}

func defaultAssignment(declaration Declaration) (func(), error) {
	target, err := targetValue(declaration)
	if err != nil {
		return nil, err
	}
	defaultValue := reflect.ValueOf(declaration.defaultValue)
	if !defaultValue.IsValid() || !defaultValue.Type().AssignableTo(target.Type()) {
		return nil, fmt.Errorf("%s has an invalid default", declaration.name)
	}
	return func() {
		target.Set(defaultValue)
	}, nil
}

func parsedAssignment(declaration Declaration, raw string) (func(), error) {
	target, err := targetValue(declaration)
	if err != nil {
		return nil, err
	}

	parsed := reflect.New(target.Type()).Elem()
	switch {
	case target.Type() == reflect.TypeFor[time.Duration]():
		value, parseErr := time.ParseDuration(raw)
		if parseErr != nil {
			return nil, expectedTypeError(declaration.name, "duration")
		}
		parsed.SetInt(int64(value))
	case target.Type() == reflect.TypeFor[url.URL]():
		value, parseErr := url.Parse(raw)
		if parseErr != nil || value.Scheme == "" {
			return nil, expectedTypeError(declaration.name, "absolute URL")
		}
		parsed.Set(reflect.ValueOf(*value))
	case target.Kind() == reflect.String:
		parsed.SetString(raw)
	case target.Type() == reflect.TypeFor[[]string]():
		// A comma-separated list, because the values that need it here are
		// path prefixes and origins, and neither may contain a comma.
		items := make([]string, 0)
		for _, item := range strings.Split(raw, ",") {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			items = append(items, trimmed)
		}
		parsed.Set(reflect.ValueOf(items))
	case target.Kind() == reflect.Bool:
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return nil, expectedTypeError(declaration.name, "boolean")
		}
		parsed.SetBool(value)
	case target.Kind() >= reflect.Int && target.Kind() <= reflect.Int64:
		value, parseErr := strconv.ParseInt(raw, 10, target.Type().Bits())
		if parseErr != nil {
			return nil, expectedTypeError(declaration.name, "integer")
		}
		parsed.SetInt(value)
	default:
		return nil, fmt.Errorf(
			"%s uses unsupported type %s",
			declaration.name,
			target.Type(),
		)
	}

	return func() {
		target.Set(parsed)
	}, nil
}

func targetValue(declaration Declaration) (reflect.Value, error) {
	target := reflect.ValueOf(declaration.target)
	if !target.IsValid() || target.Kind() != reflect.Pointer || target.IsNil() {
		return reflect.Value{}, fmt.Errorf("%s destination must be a non-nil pointer", declaration.name)
	}
	return target.Elem(), nil
}

func expectedTypeError(name, expected string) error {
	return fmt.Errorf("%s must be a valid %s", name, expected)
}

func processEnvironment() map[string]string {
	environment := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			environment[name] = value
		}
	}
	return environment
}

func cloneEnvironment(environment map[string]string) map[string]string {
	clone := make(map[string]string, len(environment))
	for name, value := range environment {
		clone[name] = value
	}
	return clone
}
