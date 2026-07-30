package management

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/migrations"
	gdproject "github.com/bon5co/godjango/project"
)

type commandSettings struct{}

func (commandSettings) Validate() error { return nil }

type commandApp string

func (app commandApp) Name() string { return string(app) }

type fakeMigrationManager struct {
	applied []string
	status  []migrations.Status
}

func (manager *fakeMigrationManager) Apply(context.Context) ([]string, error) {
	return append([]string(nil), manager.applied...), nil
}

func (manager *fakeMigrationManager) Status(context.Context) ([]migrations.Status, error) {
	return append([]migrations.Status(nil), manager.status...), nil
}

type fakeUserManager struct {
	superuser auth.CreateUserOptions
	changed   [2]string
}

func (manager *fakeUserManager) CreateSuperuser(
	_ context.Context,
	options auth.CreateUserOptions,
) (*auth.User, error) {
	manager.superuser = options
	return &auth.User{Username: options.Username}, nil
}

func (manager *fakeUserManager) ChangePassword(
	_ context.Context,
	username string,
	password string,
) error {
	manager.changed = [2]string{username, password}
	return nil
}

func TestProjectMigrationCommandsAreLazyAndAlwaysCleanUp(t *testing.T) {
	root := newFixtureProject(t, "example.com/bookshelf", map[string]string{})
	configured, err := gdproject.New(commandSettings{}, commandApp("library"))
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeMigrationManager{
		applied: []string{"20260731010101_create_books"},
		status: []migrations.Status{
			{Name: "20260731010101_create_books", Applied: true},
			{Name: "20260731010202_add_author", Applied: false},
		},
	}
	opened := 0
	cleaned := 0
	options := ProjectOptions{
		Project:          configured,
		WorkingDirectory: root,
		Services: ProjectServices{
			Migrations: func(context.Context) (MigrationManager, func() error, error) {
				opened++
				return manager, func() error {
					cleaned++
					return nil
				}, nil
			},
		},
	}

	for _, test := range []struct {
		args     []string
		contains []string
	}{
		{args: []string{"migrate"}, contains: []string{"20260731010101_create_books"}},
		{
			args:     []string{"migrationstatus"},
			contains: []string{"[X] 20260731010101_create_books", "[ ] 20260731010202_add_author"},
		},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := ExecuteProject(
			context.Background(),
			test.args,
			options,
			Streams{Out: &stdout, Err: &stderr},
		); code != ExitOK {
			t.Fatalf("%v exit = %d; stderr=%q", test.args, code, stderr.String())
		}
		for _, fragment := range test.contains {
			if !strings.Contains(stdout.String(), fragment) {
				t.Fatalf("%v stdout %q does not contain %q", test.args, stdout.String(), fragment)
			}
		}
	}
	if opened != 2 || cleaned != 2 {
		t.Fatalf("services opened=%d cleaned=%d, want 2/2", opened, cleaned)
	}
}

func TestMakeMigrationCreatesExplicitPairForRegisteredApp(t *testing.T) {
	root := newFixtureProject(t, "example.com/bookshelf", map[string]string{})
	if err := os.MkdirAll(filepath.Join(root, "apps", "library", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	configured, err := gdproject.New(commandSettings{}, commandApp("library"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ExecuteProject(
		context.Background(),
		[]string{"makemigration", "create_books", "--app", "library"},
		ProjectOptions{
			Project:          configured,
			WorkingDirectory: root,
			MigrationScaffolder: migrations.Scaffolder{
				Now: func() time.Time { return now },
			},
		},
		Streams{Out: &stdout, Err: &stderr},
	)
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	for _, suffix := range []string{"up.sql", "down.sql"} {
		path := filepath.Join(
			root,
			"apps",
			"library",
			"migrations",
			"20260731010203_create_books.tx."+suffix,
		)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
	if !strings.Contains(stdout.String(), "explicit SQL") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestUserCommandsUseSharedManagerAndKeepPasswordsOutOfOutput(t *testing.T) {
	root := newFixtureProject(t, "example.com/bookshelf", map[string]string{})
	configured, err := gdproject.New(commandSettings{})
	if err != nil {
		t.Fatal(err)
	}
	users := new(fakeUserManager)
	opened := 0
	cleaned := 0
	options := ProjectOptions{
		Project:          configured,
		WorkingDirectory: root,
		Services: ProjectServices{
			Users: func(context.Context) (UserManager, func() error, error) {
				opened++
				return users, func() error {
					cleaned++
					return nil
				}, nil
			},
		},
	}

	for _, test := range []struct {
		args  []string
		input string
	}{
		{
			args:  []string{"createsuperuser", "--username", "root", "--email", "ROOT@EXAMPLE.COM", "--password-stdin"},
			input: "super-secret\n",
		},
		{
			args:  []string{"changepassword", "alice", "--password-stdin"},
			input: "new-secret\n",
		},
	} {
		var output bytes.Buffer
		code := ExecuteProject(
			context.Background(),
			test.args,
			options,
			Streams{In: strings.NewReader(test.input), Out: &output, Err: &output},
		)
		if code != ExitOK {
			t.Fatalf("%v exit = %d; output=%q", test.args, code, output.String())
		}
		if strings.Contains(output.String(), strings.TrimSpace(test.input)) {
			t.Fatalf("%v leaked password in %q", test.args, output.String())
		}
	}
	if users.superuser.Username != "root" ||
		users.superuser.Email != "ROOT@EXAMPLE.COM" ||
		users.superuser.Password == nil ||
		*users.superuser.Password != "super-secret" ||
		!users.superuser.IsStaff ||
		!users.superuser.IsSuperuser {
		t.Fatalf("superuser options = %#v", users.superuser)
	}
	if !reflect.DeepEqual(users.changed, [2]string{"alice", "new-secret"}) {
		t.Fatalf("password change = %#v", users.changed)
	}
	if opened != 2 || cleaned != 2 {
		t.Fatalf("services opened=%d cleaned=%d, want 2/2", opened, cleaned)
	}
}

func TestRuntimeAndDatabaseShellCommandsUseExplicitServices(t *testing.T) {
	root := newFixtureProject(t, "example.com/bookshelf", map[string]string{})
	var serverArgs []string
	var shellArgs []string
	options := ProjectOptions{
		WorkingDirectory: root,
		Services: ProjectServices{
			RunServer: func(_ context.Context, args []string, _ Streams) error {
				serverArgs = append([]string(nil), args...)
				return nil
			},
			DatabaseShell: func(_ context.Context, args []string, _ Streams) error {
				shellArgs = append([]string(nil), args...)
				return nil
			},
		},
	}

	for _, args := range [][]string{
		{"runserver", "127.0.0.1:9000"},
		{"dbshell", "--", "-c", "select 1"},
	} {
		if code := ExecuteProject(
			context.Background(),
			args,
			options,
			Streams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}},
		); code != ExitOK {
			t.Fatalf("%v exit = %d", args, code)
		}
	}
	if !reflect.DeepEqual(serverArgs, []string{"127.0.0.1:9000"}) {
		t.Fatalf("server args = %#v", serverArgs)
	}
	if !reflect.DeepEqual(shellArgs, []string{"--", "-c", "select 1"}) {
		t.Fatalf("shell args = %#v", shellArgs)
	}
}

func TestProjectOperationalFailuresReturnFailureAndCleanUp(t *testing.T) {
	root := newFixtureProject(t, "example.com/bookshelf", map[string]string{})
	cleanupErr := errors.New("close failed")
	cleaned := false
	var output bytes.Buffer
	code := ExecuteProject(
		context.Background(),
		[]string{"migrate"},
		ProjectOptions{
			WorkingDirectory: root,
			Services: ProjectServices{
				Migrations: func(context.Context) (MigrationManager, func() error, error) {
					return &fakeMigrationManager{}, func() error {
						cleaned = true
						return cleanupErr
					}, nil
				},
			},
		},
		Streams{Out: &output, Err: &output},
	)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d; output=%q", code, ExitFailure, output.String())
	}
	if !cleaned || !strings.Contains(output.String(), cleanupErr.Error()) {
		t.Fatalf("cleaned=%v output=%q", cleaned, output.String())
	}
}

func TestProjectHelpListsDjangoStyleBuiltIns(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ExecuteProject(
		context.Background(),
		[]string{"--help"},
		ProjectOptions{WorkingDirectory: t.TempDir()},
		Streams{Out: &stdout, Err: &stderr},
	)
	if code != ExitOK {
		t.Fatalf("exit = %d; stderr=%q", code, stderr.String())
	}
	for _, command := range []string{
		"changepassword",
		"check",
		"createsuperuser",
		"dbshell",
		"makemigration",
		"migrate",
		"migrationstatus",
		"runserver",
		"startapp",
		"test",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Errorf("help %q does not contain %q", stdout.String(), command)
		}
	}
}
