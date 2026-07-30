package management

import (
	"context"
	"errors"
	"fmt"
	"os"

	gdproject "github.com/bon5co/godjango/project"
	"github.com/spf13/cobra"
)

type Command struct {
	Name    string
	Summary string
	Run     func(context.Context, []string, Streams) error
}

type ProjectOptions struct {
	Project          *gdproject.Project
	WorkingDirectory string
	Scaffolder       Scaffolder
	Commands         []Command
}

// ExecuteProject runs the command registry compiled into cmd/manage.
func ExecuteProject(ctx context.Context, args []string, options ProjectOptions, streams Streams) int {
	streams = streams.withDefaults()
	workingDirectory := options.WorkingDirectory
	if workingDirectory == "" {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintln(streams.Err, err)
			return ExitFailure
		}
	}

	root := &cobra.Command{
		Use:           "manage",
		Short:         "GoDjangGo project management",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetArgs(args)
	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)

	testCommand := &cobra.Command{
		Use:                "test [packages] [-- go-test-arguments]",
		Short:              "Run project unit tests",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, commandArgs []string) error {
			return RunUnitTests(ctx, workingDirectory, commandArgs, streams)
		},
	}
	root.AddCommand(testCommand)

	checkCommand := &cobra.Command{
		Use:   "check",
		Short: "Run project and application checks",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if options.Project == nil {
				return errors.New("godjango check: project configuration is unavailable")
			}
			if err := options.Project.Check(ctx); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(streams.Out, "System check identified no issues.")
			return nil
		},
	}
	root.AddCommand(checkCommand)

	startAppCommand := &cobra.Command{
		Use:   "startapp <name>",
		Short: "Create and explicitly register an application",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, commandArgs []string) error {
			projectRoot, err := DiscoverProject(workingDirectory)
			if err != nil {
				return err
			}
			if err := options.Scaffolder.StartApp(projectRoot, commandArgs[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(streams.Out, "Created app %s\n", commandArgs[0])
			return nil
		},
	}
	root.AddCommand(startAppCommand)

	reserved := map[string]struct{}{
		"check":    {},
		"help":     {},
		"startapp": {},
		"test":     {},
	}
	for _, registered := range options.Commands {
		if registered.Name == "" || registered.Run == nil {
			_, _ = fmt.Fprintln(streams.Err, "godjango: custom commands require a name and runner")
			return ExitUsage
		}
		if _, conflict := reserved[registered.Name]; conflict {
			_, _ = fmt.Fprintf(streams.Err, "godjango: command %q is already registered\n", registered.Name)
			return ExitUsage
		}
		reserved[registered.Name] = struct{}{}
		command := registered
		root.AddCommand(&cobra.Command{
			Use:                command.Name,
			Short:              command.Summary,
			DisableFlagParsing: true,
			RunE: func(_ *cobra.Command, commandArgs []string) error {
				return command.Run(ctx, commandArgs, streams)
			},
		})
	}

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	_, _ = fmt.Fprintln(streams.Err, err)
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	if errors.Is(err, context.Canceled) {
		return ExitCanceled
	}
	return ExitUsage
}
