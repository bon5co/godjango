package env_test

import (
	"testing"

	"github.com/bon5co/godjango/env"
)

func TestOptionalStringListParsesCommaSeparatedValues(t *testing.T) {
	var paths []string
	schema := env.New(env.Optional("STATELESS_PATHS", &paths, nil))

	err := schema.Load(
		env.WithEnvironment(map[string]string{"STATELESS_PATHS": "/api, /healthz ,"}),
		env.WithoutDotEnv(),
	)
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if len(paths) != 2 || paths[0] != "/api" || paths[1] != "/healthz" {
		t.Fatalf("paths = %#v, want [/api /healthz]", paths)
	}
}

func TestOptionalStringListDefaultsToDeclaredValue(t *testing.T) {
	var paths []string
	schema := env.New(env.Optional("STATELESS_PATHS", &paths, nil))

	err := schema.Load(env.WithEnvironment(map[string]string{}), env.WithoutDotEnv())
	if err != nil {
		t.Fatalf("Load() = %v, want nil", err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths = %#v, want empty", paths)
	}
}
