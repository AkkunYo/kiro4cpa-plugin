# Contributing

## Requirements

- Go 1.26.10 or newer (see `toolchain` in `go.mod`)
- A C compiler: cgo is required, the plugin is built with `-buildmode=c-shared`

## Build

```bash
make test                 # unit tests
make build-linux-docker   # cross-compile bin/linux/amd64/kiro4cpa.so with Docker
make build-darwin         # build bin/darwin/arm64/kiro4cpa.dylib (needs working Xcode CLI tools)
```

`make build-darwin` fails on some macOS hosts because the SDK's `.tbd` stubs
reject the `arm64e.x1` architecture. That is an Xcode/toolchain problem, not a
code problem; use the Docker build.

## Artifact naming

CLIProxyAPI derives the plugin ID and version from the shared library file name.
A release artifact must be named `kiro4cpa-v<version>.<so|dylib|dll>`. A bare
`kiro4cpa.so` makes the host register the plugin ID `kiro4cpa` without a version.

## Testing

```bash
gofmt -l .                        # must print nothing
go vet ./...
go test ./... -race
go test ./internal/... -cover     # internal/kiro stays above 85%
```

CI runs the tests on Linux, macOS and Windows for every push and pull request.
The release workflow runs them again before building, and reads the stamped
version back from every binary.

Static analysis:

```bash
govulncheck ./...
gosec ./internal/...
```

Both must report no issues. If a finding is intended, annotate it with
`// #nosec <rule>` and a comment saying why.

## Commits

Use Conventional Commits: `feat:`, `fix:`, `docs:`, `ci:`, `chore:`. The body
explains why the change is needed, not what the diff does.

## Release

Maintainers only:

1. Bump `internal/kiro/version.go`. The store compares versions, so it must
   increase.
2. Run the checks above.
3. Commit and push to `main`.
4. `git tag -a v<version> -m "v<version>" && git push origin v<version>`.

Pushing the tag is the release. CI builds five platforms and publishes the
GitHub Release, which is what the official plugin store reads.

## Plugin store

The plugin is listed in the official registry at
`router-for-me/CLIProxyAPI-Plugins-Store`. Publishing a new version needs only a
new tag; the registry entry does not change. Open a registry pull request only
when the plugin's name, description, logo or repository changes, or when adding
a new plugin.
