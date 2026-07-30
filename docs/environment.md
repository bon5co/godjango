# Environment declarations

Declare every application environment variable at the entry-point boundary.
Required values have no fallback. Optional values always state their default:

```go
type Settings struct {
	DatabaseURL url.URL
	Address     string
	Debug       bool
	SecretKey   env.Secret
}

var settings Settings
schema := env.New(
	env.Required("DATABASE_URL", &settings.DatabaseURL),
	env.Optional("ADDRESS", &settings.Address, ":8000"),
	env.Optional("DEBUG", &settings.Debug, false),
	env.Required("SECRET_KEY", &settings.SecretKey),
)

if err := schema.Load(); err != nil {
	return err
}
```

`Load` validates the complete schema and assigns nothing when any declaration
fails. Call it before opening a database, starting background work, or listening
for HTTP traffic. Errors report every missing or malformed variable without
printing values.

Supported destination types are strings, booleans, signed integers,
`time.Duration`, `url.URL`, and `env.Secret`. A secret prints as `[REDACTED]`;
call `Reveal` only at the integration boundary that consumes it.

## `.env` behavior

`Load` reads `.env` from the current working directory by default. Process
environment values take precedence. This supports local development without
allowing a checked-out file to override deployment configuration.

Production entry points may disable file loading explicitly:

```go
err := schema.Load(env.WithoutDotEnv())
```

Tests and isolated launchers should pass `WithEnvironment` instead of mutating
the process environment. `WithWorkingDirectory` selects an isolated `.env`
location.

Never commit a `.env` file containing secrets.
