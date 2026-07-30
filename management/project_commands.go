package management

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bon5co/godjango/auth"
	"github.com/bon5co/godjango/migrations"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type MigrationManager interface {
	Apply(context.Context) ([]string, error)
	Status(context.Context) ([]migrations.Status, error)
}

type UserManager interface {
	CreateSuperuser(context.Context, auth.CreateUserOptions) (*auth.User, error)
	ChangePassword(context.Context, string, string) error
}

type ProjectServices struct {
	Migrations    func(context.Context) (MigrationManager, func() error, error)
	Users         func(context.Context) (UserManager, func() error, error)
	RunServer     func(context.Context, []string, Streams) error
	DatabaseShell func(context.Context, []string, Streams) error
}

func addProjectServiceCommands(
	root *cobra.Command,
	ctx context.Context,
	workingDirectory string,
	options ProjectOptions,
	streams Streams,
) {
	root.AddCommand(makeMigrationCommand(workingDirectory, options, streams))
	root.AddCommand(migrateCommand(ctx, options.Services, streams))
	root.AddCommand(migrationStatusCommand(ctx, options.Services, streams))
	root.AddCommand(createSuperuserCommand(ctx, options.Services, streams))
	root.AddCommand(changePasswordCommand(ctx, options.Services, streams))
	root.AddCommand(runServerCommand(ctx, options.Services, streams))
	root.AddCommand(databaseShellCommand(ctx, options.Services, streams))
}

func makeMigrationCommand(
	workingDirectory string,
	options ProjectOptions,
	streams Streams,
) *cobra.Command {
	var appName string
	command := &cobra.Command{
		Use:   "makemigration <name>",
		Short: "Create an explicit paired SQL migration",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if options.Project == nil {
				return runtimeFailure(errors.New("godjango makemigration: project configuration is unavailable"))
			}
			root, err := DiscoverProject(workingDirectory)
			if err != nil {
				return err
			}
			selected, err := selectMigrationApp(root, workingDirectory, appName, options)
			if err != nil {
				return err
			}
			files, err := options.MigrationScaffolder.Create(
				filepath.Join(root, "apps", selected, "migrations"),
				args[0],
			)
			if err != nil {
				return runtimeFailure(err)
			}
			_, _ = fmt.Fprintf(
				streams.Out,
				"Created explicit SQL migrations:\n  %s\n  %s\n",
				files.UpPath,
				files.DownPath,
			)
			return nil
		},
	}
	command.Flags().StringVar(&appName, "app", "", "registered app that owns the migration")
	return command
}

func selectMigrationApp(root, workingDirectory, requested string, options ProjectOptions) (string, error) {
	if requested != "" {
		if _, exists := options.Project.App(requested); !exists {
			return "", fmt.Errorf("godjango makemigration: app %q is not registered", requested)
		}
		return requested, nil
	}
	absolute, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, absolute)
	if err == nil {
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) >= 2 && parts[0] == "apps" {
			if _, exists := options.Project.App(parts[1]); exists {
				return parts[1], nil
			}
		}
	}
	apps := options.Project.Apps()
	if len(apps) == 1 {
		return apps[0].Name(), nil
	}
	return "", errors.New("godjango makemigration: select a registered app with --app")
}

func migrateCommand(ctx context.Context, services ProjectServices, streams Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending migrations",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if services.Migrations == nil {
				return runtimeFailure(errors.New("godjango migrate: migration service is not configured"))
			}
			err := withService(ctx, services.Migrations, func(manager MigrationManager) error {
				names, err := manager.Apply(ctx)
				if err != nil {
					return err
				}
				if len(names) == 0 {
					_, _ = fmt.Fprintln(streams.Out, "No migrations to apply.")
					return nil
				}
				for _, name := range names {
					_, _ = fmt.Fprintf(streams.Out, "Applied %s\n", name)
				}
				return nil
			})
			if err != nil {
				return runtimeFailure(err)
			}
			return nil
		},
	}
}

func migrationStatusCommand(ctx context.Context, services ProjectServices, streams Streams) *cobra.Command {
	return &cobra.Command{
		Use:   "migrationstatus",
		Short: "Show migration status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if services.Migrations == nil {
				return runtimeFailure(errors.New("godjango migrationstatus: migration service is not configured"))
			}
			err := withService(ctx, services.Migrations, func(manager MigrationManager) error {
				statuses, err := manager.Status(ctx)
				if err != nil {
					return err
				}
				for _, status := range statuses {
					marker := " "
					if status.Applied {
						marker = "X"
					}
					_, _ = fmt.Fprintf(streams.Out, "[%s] %s\n", marker, status.Name)
				}
				return nil
			})
			if err != nil {
				return runtimeFailure(err)
			}
			return nil
		},
	}
}

func createSuperuserCommand(ctx context.Context, services ProjectServices, streams Streams) *cobra.Command {
	var username string
	var email string
	var passwordStdin bool
	var noInput bool
	command := &cobra.Command{
		Use:   "createsuperuser",
		Short: "Create an administrative user",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if username == "" {
				username = os.Getenv("GODJANGO_SUPERUSER_USERNAME")
			}
			if username == "" && !noInput {
				var err error
				username, err = promptLine(streams, "Username: ")
				if err != nil {
					return err
				}
			}
			if username == "" {
				return errors.New(
					"godjango createsuperuser: provide --username or GODJANGO_SUPERUSER_USERNAME",
				)
			}
			if email == "" {
				email = os.Getenv("GODJANGO_SUPERUSER_EMAIL")
			}
			if email == "" && !noInput {
				var err error
				email, err = promptLine(streams, "Email address: ")
				if err != nil {
					return err
				}
			}
			password, err := commandPassword(
				streams,
				passwordStdin,
				"GODJANGO_SUPERUSER_PASSWORD",
				noInput,
			)
			if err != nil {
				return err
			}
			if services.Users == nil {
				return runtimeFailure(errors.New("godjango createsuperuser: user service is not configured"))
			}
			err = withService(ctx, services.Users, func(manager UserManager) error {
				_, err := manager.CreateSuperuser(ctx, auth.CreateUserOptions{
					Username:    username,
					Email:       email,
					Password:    &password,
					IsStaff:     true,
					IsSuperuser: true,
				})
				return err
			})
			if err != nil {
				return runtimeFailure(err)
			}
			_, _ = fmt.Fprintf(streams.Out, "Superuser %s created.\n", username)
			return nil
		},
	}
	command.Flags().StringVar(&username, "username", "", "superuser username")
	command.Flags().StringVar(&email, "email", "", "superuser email")
	command.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read one password line from stdin")
	command.Flags().BoolVar(&noInput, "noinput", false, "disable interactive prompts")
	return command
}

func changePasswordCommand(ctx context.Context, services ProjectServices, streams Streams) *cobra.Command {
	var passwordStdin bool
	command := &cobra.Command{
		Use:   "changepassword <username>",
		Short: "Change a user's password",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			password, err := commandPassword(streams, passwordStdin, "GODJANGO_PASSWORD", false)
			if err != nil {
				return err
			}
			if services.Users == nil {
				return runtimeFailure(errors.New("godjango changepassword: user service is not configured"))
			}
			err = withService(ctx, services.Users, func(manager UserManager) error {
				return manager.ChangePassword(ctx, args[0], password)
			})
			if err != nil {
				return runtimeFailure(err)
			}
			_, _ = fmt.Fprintf(streams.Out, "Password changed for %s.\n", args[0])
			return nil
		},
	}
	command.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read one password line from stdin")
	return command
}

func commandPassword(
	streams Streams,
	fromStdin bool,
	environmentName string,
	noInput bool,
) (string, error) {
	if !fromStdin {
		if password, exists := os.LookupEnv(environmentName); exists {
			if password == "" {
				return "", fmt.Errorf("godjango: %s must not be empty", environmentName)
			}
			return password, nil
		}
		if noInput {
			return "", fmt.Errorf(
				"godjango: provide a password with --password-stdin or %s",
				environmentName,
			)
		}
		return promptPassword(streams)
	}
	scanner := bufio.NewScanner(streams.In)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", errors.New("godjango: password input is empty")
	}
	password := strings.TrimSuffix(scanner.Text(), "\r")
	if password == "" {
		return "", errors.New("godjango: password must not be empty")
	}
	return password, nil
}

func promptLine(streams Streams, prompt string) (string, error) {
	_, _ = fmt.Fprint(streams.Out, prompt)
	scanner := bufio.NewScanner(streams.In)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", errors.New("godjango: interactive input ended")
	}
	return strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r")), nil
}

func promptPassword(streams Streams) (string, error) {
	input, ok := streams.In.(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) {
		return "", errors.New(
			"godjango: secure password prompt requires a terminal; use --password-stdin or environment",
		)
	}
	first, err := readTerminalPassword(input, streams.Out, "Password: ")
	if err != nil {
		return "", err
	}
	second, err := readTerminalPassword(input, streams.Out, "Password (again): ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("godjango: passwords do not match")
	}
	if first == "" {
		return "", errors.New("godjango: password must not be empty")
	}
	return first, nil
}

func readTerminalPassword(input *os.File, output io.Writer, prompt string) (string, error) {
	_, _ = fmt.Fprint(output, prompt)
	password, err := term.ReadPassword(int(input.Fd()))
	_, _ = fmt.Fprintln(output)
	if err != nil {
		return "", err
	}
	return string(password), nil
}

func runServerCommand(ctx context.Context, services ProjectServices, streams Streams) *cobra.Command {
	return &cobra.Command{
		Use:                "runserver [address]",
		Short:              "Run the development server",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if services.RunServer == nil {
				return runtimeFailure(errors.New("godjango runserver: runtime service is not configured"))
			}
			if err := services.RunServer(ctx, args, streams); err != nil {
				return runtimeFailure(err)
			}
			return nil
		},
	}
}

func databaseShellCommand(ctx context.Context, services ProjectServices, streams Streams) *cobra.Command {
	return &cobra.Command{
		Use:                "dbshell [-- psql-arguments]",
		Short:              "Open a PostgreSQL shell",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if services.DatabaseShell == nil {
				return runtimeFailure(errors.New("godjango dbshell: database shell service is not configured"))
			}
			if err := services.DatabaseShell(ctx, args, streams); err != nil {
				return runtimeFailure(err)
			}
			return nil
		},
	}
}

func withService[T any](
	ctx context.Context,
	open func(context.Context) (T, func() error, error),
	run func(T) error,
) (resultErr error) {
	service, cleanup, err := open(ctx)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer func() {
			resultErr = errors.Join(resultErr, cleanup())
		}()
	}
	return run(service)
}

func runtimeFailure(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return &ExitError{Code: ExitFailure, Err: err}
}
