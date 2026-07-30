package management

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var scaffoldIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type Scaffolder struct {
	FrameworkVersion string
	// FrameworkReplace is intended for framework development and tests. Normal
	// generated projects pin FrameworkVersion without a replace directive.
	FrameworkReplace string
}

func (scaffolder Scaffolder) StartProject(ctx context.Context, parent, name string) (root string, resultErr error) {
	if !scaffoldIdentifier.MatchString(name) {
		return "", fmt.Errorf("godjango startproject: invalid project name %q", name)
	}
	if scaffolder.FrameworkVersion == "" ||
		scaffolder.FrameworkVersion == "(devel)" ||
		scaffolder.FrameworkVersion == "latest" {
		return "", errors.New("godjango startproject: a pinned framework version is required")
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	root = filepath.Join(parent, name)
	if err := os.Mkdir(root, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("godjango startproject: refusing to overwrite %s", root)
		}
		return "", err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(root))
		}
	}()

	for _, directory := range []string{
		"cmd/manage",
		"cmd/server",
		"internal/project",
		"apps",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			return "", err
		}
	}

	goMod := fmt.Sprintf(
		"module %s\n\ngo 1.26.5\n\nrequire github.com/bon5co/godjango %s\n",
		name,
		scaffolder.FrameworkVersion,
	)
	if scaffolder.FrameworkReplace != "" {
		replacement, err := filepath.Abs(scaffolder.FrameworkReplace)
		if err != nil {
			return "", err
		}
		goMod += "\nreplace github.com/bon5co/godjango => " + filepath.ToSlash(replacement) + "\n"
	}
	files := map[string]string{
		ProjectMarker: fmt.Sprintf(
			"module=%s\nframework=github.com/bon5co/godjango@%s\n",
			name,
			scaffolder.FrameworkVersion,
		),
		"go.mod": goMod,
		"cmd/manage/main.go": fmt.Sprintf(`package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bon5co/godjango/management"
	configuredproject "%s/internal/project"
)

func main() {
	configured, err := configuredproject.Configure()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(management.ExitFailure)
	}
	os.Exit(management.ExecuteProject(
		context.Background(),
		os.Args[1:],
		management.ProjectOptions{
			Project:  configured,
			Services: configuredproject.Services(),
			Commands: configuredproject.Commands(),
		},
		management.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr},
	))
}
`, name),
		"cmd/server/main.go": `package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/web"
	configuredproject "PROJECT_MODULE/internal/project"
)

func main() {
	address := "127.0.0.1:8000"
	if len(os.Args) > 1 {
		address = os.Args[1]
	}
	settings, err := configuredproject.LoadRuntimeSettings()
	if err != nil {
		exit(err)
	}
	db, err := database.Open(
		context.Background(),
		database.DefaultConfig(settings.DatabaseURL.Reveal()),
	)
	if err != nil {
		exit(err)
	}
	defer db.Close()
	configured, err := configuredproject.Configure()
	if err != nil {
		exit(err)
	}
	store := auth.NewBunStore(db)
	manager := auth.NewManager(store, auth.NewPasswordHasher())
	sessionStore, err := web.NewSessionStore(store)
	if err != nil {
		exit(err)
	}
	sessions, err := web.NewSessions(web.SessionConfig{
		CookieName: "godjango_session",
		Lifetime: 24 * time.Hour,
		IdleTimeout: 30 * time.Minute,
		Secure: !settings.Debug,
	}, sessionStore)
	if err != nil {
		exit(err)
	}
	csrf, err := web.NewCSRF(web.CSRFConfig{
		CookieName: "godjango_csrf",
		Secure: !settings.Debug,
	})
	if err != nil {
		exit(err)
	}
	sessionSecret := derive(settings.SessionSecret.Reveal(), "session")
	resetSecret := derive(settings.SessionSecret.Reveal(), "password-reset")
	router, err := web.NewRouter(web.RouterConfig{
		Project: configured,
		Middleware: []web.Middleware{
			web.RequestID(),
			web.Recover(),
			web.SecurityHeaders(web.SecurityHeadersConfig{HTTPS: !settings.Debug}),
			web.BodyLimit(1 << 20),
			sessions.Middleware,
			csrf.Middleware,
			web.Authentication(manager, sessionSecret),
		},
	})
	if err != nil {
		exit(err)
	}
	authHandlers, err := web.NewAuthHandlers(web.AuthHandlerConfig{
		Backend: manager,
		SessionSecret: sessionSecret,
		ResetTokens: auth.ResetTokenGenerator{
			Secret: resetSecret,
			Timeout: 24 * time.Hour,
		},
		SendReset: func(context.Context, web.ResetMessage) error {
			return errors.New("password reset delivery is not configured")
		},
	})
	if err != nil {
		exit(err)
	}
	authHandlers.Routes(router)
	router.Get("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	listener, err := net.Listen("tcp", address)
	if err != nil {
		exit(err)
	}
	fmt.Fprintf(os.Stdout, "Starting development server at http://%s/\n", address)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := web.Server{
		Handler: router,
		ShutdownTimeout: 10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute,
	}
	if err := server.Serve(ctx, listener); err != nil {
		exit(err)
	}
}

func derive(secret string, purpose string) []byte {
	sum := sha256.Sum256([]byte(purpose + "\x00" + secret))
	return sum[:]
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
`,
		"internal/project/settings.go": fmt.Sprintf(`package project

import gdproject "github.com/bon5co/godjango/project"

type Settings struct{}

func (Settings) Validate() error { return nil }

func Configure() (*gdproject.Project, error) {
	return gdproject.New(Settings{}, Apps()...)
}
`),
		"internal/project/services.go": generatedServicesSource,
	}
	for fileName, content := range files {
		content = strings.ReplaceAll(content, "PROJECT_MODULE", name)
		if strings.HasSuffix(fileName, ".go") {
			formatted, err := format.Source([]byte(content))
			if err != nil {
				return "", fmt.Errorf("format generated %s: %w", fileName, err)
			}
			content = string(formatted)
		}
		if err := writeNew(filepath.Join(root, filepath.FromSlash(fileName)), content); err != nil {
			return "", err
		}
	}
	if err := writeAppRegistry(root, name, nil); err != nil {
		return "", err
	}
	if err := runGoModTidy(ctx, root); err != nil {
		return "", err
	}
	return root, nil
}

func (scaffolder Scaffolder) StartApp(root, name string) error {
	if !scaffoldIdentifier.MatchString(name) {
		return fmt.Errorf("godjango startapp: invalid app name %q", name)
	}
	projectRoot, err := DiscoverProject(root)
	if err != nil {
		return err
	}
	module, err := moduleName(projectRoot)
	if err != nil {
		return err
	}
	appRoot := filepath.Join(projectRoot, "apps", name)
	if err := os.Mkdir(appRoot, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("godjango startapp: app %q already exists", name)
		}
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(appRoot)
		}
	}()

	for _, directory := range []string{"migrations", "templates"} {
		if err := os.Mkdir(filepath.Join(appRoot, directory), 0o755); err != nil {
			return err
		}
	}
	appSource := fmt.Sprintf(`package %s

import (
	"embed"
	"io/fs"
)

type App struct{}

func New() *App { return &App{} }

func (*App) Name() string { return %q }

//go:embed migrations
var migrationFiles embed.FS

func (*App) MigrationFS() fs.FS {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return files
}
`, name, name)
	files := map[string]string{
		"app.go":    appSource,
		"models.go": "package " + name + "\n",
		"routes.go": `package ` + name + `

import "github.com/go-chi/chi/v5"

func (*App) Routes(router chi.Router) {
	RegisterRoutes(router)
}

func RegisterRoutes(chi.Router) {}
`,
		"commands.go": `package ` + name + `

import "github.com/bon5co/godjango/management"

func Commands() []management.Command { return nil }
`,
		"migrations/README.md": "# Explicit paired SQL migrations for this app.\n",
	}
	for fileName, content := range files {
		if strings.HasSuffix(fileName, ".go") {
			formatted, formatErr := format.Source([]byte(content))
			if formatErr != nil {
				return formatErr
			}
			content = string(formatted)
		}
		if err := writeNew(filepath.Join(appRoot, filepath.FromSlash(fileName)), content); err != nil {
			return err
		}
	}

	apps, err := appDirectories(projectRoot)
	if err != nil {
		return err
	}
	if err := writeAppRegistry(projectRoot, module, apps); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func writeAppRegistry(root, module string, apps []string) error {
	sort.Strings(apps)
	var imports strings.Builder
	var values strings.Builder
	var commands strings.Builder
	for _, app := range apps {
		_, _ = fmt.Fprintf(&imports, "\t%q\n", module+"/apps/"+app)
		_, _ = fmt.Fprintf(&values, "\t\t%s.New(),\n", app)
		_, _ = fmt.Fprintf(&commands, "\tregistered = append(registered, %s.Commands()...)\n", app)
	}
	source := fmt.Sprintf(`package project

import (
	"github.com/bon5co/godjango/auth"
	gdproject "github.com/bon5co/godjango/project"
%s)

func Apps() []gdproject.App {
	return []gdproject.App{
		auth.App,
%s	}
}
`, imports.String(), values.String())
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return err
	}
	path := filepath.Join(root, "internal", "project", "apps.go")
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return err
	}

	commandSource := fmt.Sprintf(`package project

import (
	"github.com/bon5co/godjango/management"
%s)

func Commands() []management.Command {
	var registered []management.Command
%s	return registered
}
`, imports.String(), commands.String())
	formattedCommands, err := format.Source([]byte(commandSource))
	if err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(root, "internal", "project", "commands.go"),
		formattedCommands,
		0o644,
	)
}

func appDirectories(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "apps"))
	if err != nil {
		return nil, err
	}
	var apps []string
	for _, entry := range entries {
		if entry.IsDir() && scaffoldIdentifier.MatchString(entry.Name()) {
			if _, err := os.Stat(filepath.Join(root, "apps", entry.Name(), "app.go")); err == nil {
				apps = append(apps, entry.Name())
			}
		}
	}
	sort.Strings(apps)
	return apps, nil
}

func moduleName(root string) (string, error) {
	content, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", errors.New("godjango: project go.mod has no module declaration")
}

func runGoModTidy(ctx context.Context, root string) error {
	command := exec.CommandContext(ctx, "go", "mod", "tidy")
	command.Dir = root
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("godjango startproject: go mod tidy: %w\n%s", err, output)
	}
	return nil
}

func writeNew(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

const generatedServicesSource = `package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/database"
	"github.com/bon5co/godjango/env"
	"github.com/bon5co/godjango/management"
	"github.com/bon5co/godjango/migrations"
)

type DatabaseSettings struct {
	DatabaseURL env.Secret
}

type RuntimeSettings struct {
	DatabaseURL  env.Secret
	SessionSecret env.Secret
	Debug       bool
	Port        int
}

func LoadDatabaseSettings() (DatabaseSettings, error) {
	root, err := management.DiscoverProject(".")
	if err != nil {
		return DatabaseSettings{}, err
	}
	var settings DatabaseSettings
	schema := env.New(
		env.Required("DATABASE_URL", &settings.DatabaseURL),
	)
	if err := schema.Load(env.WithWorkingDirectory(root)); err != nil {
		return DatabaseSettings{}, err
	}
	return settings, nil
}

func LoadRuntimeSettings() (RuntimeSettings, error) {
	root, err := management.DiscoverProject(".")
	if err != nil {
		return RuntimeSettings{}, err
	}
	var settings RuntimeSettings
	schema := env.New(
		env.Required("DATABASE_URL", &settings.DatabaseURL),
		env.Required("SESSION_SECRET", &settings.SessionSecret),
		env.Optional("DEBUG", &settings.Debug, false),
		env.Optional("PORT", &settings.Port, 8000),
	)
	if err := schema.Load(env.WithWorkingDirectory(root)); err != nil {
		return RuntimeSettings{}, err
	}
	return settings, nil
}

func Services() management.ProjectServices {
	return management.ProjectServices{
		Migrations: openMigrations,
		Users: openUsers,
		RunServer: func(
			ctx context.Context,
			args []string,
			streams management.Streams,
		) error {
			settings, err := LoadRuntimeSettings()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				args = []string{fmt.Sprintf("127.0.0.1:%d", settings.Port)}
			}
			root, err := management.DiscoverProject(".")
			if err != nil {
				return err
			}
			return management.RunProjectProgram(ctx, root, "./cmd/server", args, streams)
		},
		DatabaseShell: func(
			ctx context.Context,
			args []string,
			streams management.Streams,
		) error {
			settings, err := LoadDatabaseSettings()
			if err != nil {
				return err
			}
			return management.RunDatabaseShell(ctx, settings.DatabaseURL.Reveal(), args, streams)
		},
	}
}

func openDatabase(ctx context.Context) (*database.DB, error) {
	settings, err := LoadDatabaseSettings()
	if err != nil {
		return nil, err
	}
	return database.Open(ctx, database.DefaultConfig(settings.DatabaseURL.Reveal()))
}

func openMigrations(
	ctx context.Context,
) (management.MigrationManager, func() error, error) {
	db, err := openDatabase(ctx)
	if err != nil {
		return nil, nil, err
	}
	configured, err := Configure()
	if err != nil {
		return nil, nil, errors.Join(err, db.Close())
	}
	catalog, err := migrations.Collect(configured)
	if err != nil {
		return nil, nil, errors.Join(err, db.Close())
	}
	runner, err := migrations.NewRunner(db, catalog, migrations.DefaultRunnerConfig())
	if err != nil {
		return nil, nil, errors.Join(err, db.Close())
	}
	return runner, db.Close, nil
}

func openUsers(
	ctx context.Context,
) (management.UserManager, func() error, error) {
	db, err := openDatabase(ctx)
	if err != nil {
		return nil, nil, err
	}
	manager := auth.NewManager(auth.NewBunStore(db), auth.NewPasswordHasher())
	return manager, db.Close, nil
}
`
