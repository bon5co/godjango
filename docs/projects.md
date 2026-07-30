# Project settings and apps

A GoDjangGo project is compiled from project-owned settings and an explicit,
ordered list of applications:

```go
configured, err := project.New(
	settings,
	books.App,
	accounts.App,
)
if err != nil {
	return err
}
if err := configured.Check(ctx); err != nil {
	return err
}
```

Settings implement one validation method:

```go
type Settings struct {
	Environment string
	SecretKey   env.Secret
}

func (settings Settings) Validate() error {
	if settings.SecretKey.Reveal() == "" {
		return errors.New("secret key is required")
	}
	return nil
}
```

Validation happens before registry construction. Generated entry points load
the environment, construct settings, build the project, and run checks before
opening a database, starting background work, or listening for HTTP traffic.

Each app implements a stable lowercase identity:

```go
type App struct{}

func (App) Name() string { return "books" }
```

Registration order is preserved. Duplicate or unstable names fail
construction. There is no package scanning, Go plugin loading, or hidden
`init()` registration.

Optional capabilities use narrow interfaces owned by their subsystem.
`project.CheckProvider` is the first example. The migration, HTTP, template,
permission, and management-command packages will define their own provider
interfaces when those packages land; the core registry does not predeclare
generic placeholders for them.
