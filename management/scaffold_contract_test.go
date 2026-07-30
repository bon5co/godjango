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

func runGo(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
