package management

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartProjectCreatesBuildablePinnedProject(t *testing.T) {
	parent := t.TempDir()
	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scaffolder := Scaffolder{
		FrameworkVersion: "v0.0.0",
		FrameworkReplace: frameworkRoot,
	}

	root, err := scaffolder.StartProject(context.Background(), parent, "bookshelf")
	if err != nil {
		t.Fatalf("StartProject: %v", err)
	}
	for _, name := range []string{
		ProjectMarker,
		"go.mod",
		"cmd/manage/main.go",
		"cmd/server/main.go",
		"internal/project/apps.go",
		"internal/project/commands.go",
		"internal/project/settings.go",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"module bookshelf",
		"github.com/bon5co/godjango v0.0.0",
		"replace github.com/bon5co/godjango => " + frameworkRoot,
	} {
		if !strings.Contains(string(module), fragment) {
			t.Fatalf("go.mod %q does not contain %q", module, fragment)
		}
	}
	server, err := os.ReadFile(filepath.Join(root, "cmd", "server", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"Services: web.RuntimeServices{",
		"Database:",
		"Users:",
		"AuthStore:",
		"Login:",
	} {
		if !strings.Contains(string(server), fragment) {
			t.Errorf("generated server missing runtime dependency %q", fragment)
		}
	}
	// How the application is assembled belongs in the settings file, where it
	// is reviewed, diffed and tested alongside the code it configures, not in
	// the environment. Which paths skip authentication is the clearest case:
	// it is a security decision, and an environment variable carries no diff.
	projectSettings, err := os.ReadFile(filepath.Join(root, "internal", "project", "settings.go"))
	if err != nil {
		t.Fatal(err)
	}
	settingsSource := string(projectSettings)
	for _, fragment := range []string{
		"func StatelessPaths() web.StatelessPaths",
		"func Middleware(runtime RuntimeSettings, services HTTPServices) []web.Middleware",
		"stateless.Exempt(services.Sessions.Middleware)",
		"stateless.Exempt(services.CSRF.Middleware)",
		"stateless.Exempt(web.Authentication(services.Users, services.SessionSecret))",
		"TRUST_PROXY_HEADERS",
	} {
		if !strings.Contains(settingsSource, fragment) {
			t.Errorf("generated settings missing %q", fragment)
		}
	}
	if strings.Contains(settingsSource, "STATELESS_PATHS") {
		t.Error("stateless paths are read from the environment instead of source")
	}
	// Without this middleware the generated application cannot learn that a
	// proxy terminated TLS in front of it, and every POST form it serves is
	// refused as cross-origin once it is deployed. It has to resolve the
	// scheme before CSRF reads it.
	trustedProxy := strings.Index(settingsSource, "web.TrustedProxy(web.TrustedProxyConfig{")
	if trustedProxy < 0 {
		t.Error("generated settings do not decide whether to trust forwarding headers")
	}
	if csrf := strings.Index(settingsSource, "services.CSRF.Middleware"); trustedProxy > csrf {
		t.Error("generated settings resolve the request scheme after CSRF reads it")
	}
	if !strings.Contains(string(server), "configuredproject.Middleware(settings") {
		t.Error("generated server does not take its middleware chain from the settings file")
	}
	if !strings.Contains(string(server), "configuredproject.StatelessPaths().Validate()") {
		t.Error("generated server does not validate its stateless path prefixes at startup")
	}

	runGo(t, root, "test", "./...")
	runGo(t, root, "vet", "./...")
}

func TestStartProjectRefusesInvalidNamesAndExistingTargets(t *testing.T) {
	parent := t.TempDir()
	scaffolder := Scaffolder{FrameworkVersion: "v1.2.3"}

	for _, name := range []string{"", "BookShelf", "../escape", "has-dash"} {
		if _, err := scaffolder.StartProject(context.Background(), parent, name); err == nil {
			t.Errorf("StartProject(%q) succeeded", name)
		}
	}

	target := filepath.Join(parent, "bookshelf")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffolder.StartProject(context.Background(), parent, "bookshelf"); err == nil {
		t.Fatal("StartProject overwrote an existing directory")
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
		t.Fatalf("existing target changed: content=%q err=%v", content, err)
	}
}

func TestStartAppCreatesBuildableAppAndDeterministicRegistry(t *testing.T) {
	parent := t.TempDir()
	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scaffolder := Scaffolder{
		FrameworkVersion: "v0.0.0",
		FrameworkReplace: frameworkRoot,
	}
	root, err := scaffolder.StartProject(context.Background(), parent, "bookshelf")
	if err != nil {
		t.Fatal(err)
	}

	if err := scaffolder.StartApp(root, "library"); err != nil {
		t.Fatalf("StartApp(library): %v", err)
	}
	if err := scaffolder.StartApp(root, "accounts"); err != nil {
		t.Fatalf("StartApp(accounts): %v", err)
	}
	if err := scaffolder.StartApp(root, "library"); err == nil {
		t.Fatal("duplicate StartApp(library) succeeded")
	}

	registry, err := os.ReadFile(filepath.Join(root, "internal", "project", "apps.go"))
	if err != nil {
		t.Fatal(err)
	}
	accounts := strings.Index(string(registry), `"bookshelf/apps/accounts"`)
	library := strings.Index(string(registry), `"bookshelf/apps/library"`)
	if accounts < 0 || library < 0 || accounts > library {
		t.Fatalf("app imports are not deterministic: %s", registry)
	}
	for _, name := range []string{
		"apps/accounts/app.go",
		"apps/accounts/models.go",
		"apps/accounts/routes.go",
		"apps/accounts/commands.go",
		"apps/accounts/migrations",
		"apps/accounts/templates",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	runGo(t, root, "test", "./...")
	runGo(t, root, "vet", "./...")
}

func TestGeneratedProjectRunsUnitTestsThroughGlobalCLI(t *testing.T) {
	parent := t.TempDir()
	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scaffolder := Scaffolder{
		FrameworkVersion: "v0.0.0",
		FrameworkReplace: frameworkRoot,
	}
	root, err := scaffolder.StartProject(context.Background(), parent, "bookshelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffolder.StartApp(root, "library"); err != nil {
		t.Fatal(err)
	}
	testPath := filepath.Join(root, "apps", "library", "library_test.go")
	if err := os.WriteFile(testPath, []byte(`package library

import "testing"

func TestGeneratedProject(t *testing.T) {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	nested := filepath.Join(root, "apps", "library")
	code := ExecuteGlobal(
		context.Background(),
		[]string{"test", "--", "-count=1", "./apps/library/..."},
		GlobalOptions{WorkingDirectory: nested},
		Streams{Out: &stdout, Err: &stderr},
	)
	if code != ExitOK {
		t.Fatalf("exit code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "bookshelf/apps/library") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	if err := os.WriteFile(testPath, []byte(`package library

import "testing"

func TestGeneratedProject(t *testing.T) { t.Fatal("generated failure") }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = ExecuteGlobal(
		context.Background(),
		[]string{"test", "--", "-count=1", "./apps/library/..."},
		GlobalOptions{WorkingDirectory: root},
		Streams{Out: &stdout, Err: &stderr},
	)
	if code != ExitFailure {
		t.Fatalf("failure exit code = %d, want %d; stdout=%q stderr=%q", code, ExitFailure, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "generated failure") {
		t.Fatalf("failure stdout = %q", stdout.String())
	}
}

func TestGeneratedProjectRunsExplicitCustomCommandThroughBothCLILayers(t *testing.T) {
	parent := t.TempDir()
	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scaffolder := Scaffolder{
		FrameworkVersion: "v0.0.0",
		FrameworkReplace: frameworkRoot,
	}
	root, err := scaffolder.StartProject(context.Background(), parent, "bookshelf")
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffolder.StartApp(root, "library"); err != nil {
		t.Fatal(err)
	}
	commandSource := `package library

import (
	"context"
	"fmt"

	"github.com/bon5co/godjango/management"
)

func Commands(services management.ProjectServices) []management.Command {
	return []management.Command{{
		Name: "hello",
		Summary: "Run a fixture command",
		Run: func(_ context.Context, args []string, streams management.Streams) error {
			if services.Database == nil {
				return fmt.Errorf("database service was not passed to the app")
			}
			fmt.Fprintf(streams.Out, "hello %s\n", args[0])
			return nil
		},
	}}
}
`
	if err := os.WriteFile(
		filepath.Join(root, "apps", "library", "commands.go"),
		[]byte(commandSource),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ExecuteGlobal(
		context.Background(),
		[]string{"hello", "Handler"},
		GlobalOptions{WorkingDirectory: filepath.Join(root, "apps", "library")},
		Streams{Out: &stdout, Err: &stderr},
	)
	if code != ExitOK {
		t.Fatalf("exit = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "hello Handler" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestGeneratedRuntimeFailsBeforeStartWhenRequiredEnvironmentIsMissing(t *testing.T) {
	original, existed := os.LookupEnv("DATABASE_URL")
	if err := os.Unsetenv("DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("DATABASE_URL", original)
		} else {
			_ = os.Unsetenv("DATABASE_URL")
		}
	})

	frameworkRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := (Scaffolder{
		FrameworkVersion: "v0.0.0",
		FrameworkReplace: frameworkRoot,
	}).StartProject(context.Background(), t.TempDir(), "bookshelf")
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	code := ExecuteGlobal(
		context.Background(),
		[]string{"runserver"},
		GlobalOptions{WorkingDirectory: root},
		Streams{Out: &output, Err: &output},
	)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d; output=%q", code, ExitFailure, output.String())
	}
	if !strings.Contains(output.String(), "DATABASE_URL is required") {
		t.Fatalf("output = %q", output.String())
	}
}

func runGo(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
