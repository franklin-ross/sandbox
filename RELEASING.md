# Releasing

This project uses GitHub Actions to build and publish releases. Pushing a
semver-style Git tag triggers a multi-platform build and creates a GitHub
Release with attached binaries.

## Prerequisites

- All changes merged to `main`
- CI passing on `main` (lint + unit tests)

## Steps

### 1. Verify the build locally

Run the full install pipeline, which lints, builds, and runs both unit and
integration tests:

```bash
task install
```

### 2. Tag the release

Use [semantic versioning](https://semver.org). Tags must start with `v`.

```bash
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

### 3. Wait for the release workflow

Pushing the tag triggers `.github/workflows/release.yml`, which:

1. Checks out the tagged commit
2. Cross-compiles for four platforms:
   - `sandbox-darwin-arm64` (macOS Apple Silicon)
   - `sandbox-darwin-amd64` (macOS Intel)
   - `sandbox-linux-amd64`
   - `sandbox-linux-arm64`
3. Creates a GitHub Release with auto-generated release notes
4. Attaches all four binaries as release assets

### 4. Verify the release

Check the [Releases](../../releases) page to confirm all four binaries are
attached and the release notes look correct.

## CI pipeline

Every push to `main` and every pull request runs the CI workflow
(`.github/workflows/ci.yml`):

- `go vet ./...` &mdash; static analysis
- `go test -short ./...` &mdash; unit tests

## Local task reference

| Task               | Description                                        |
| ------------------ | -------------------------------------------------- |
| `task build`       | Build CLI binary + Docker image                    |
| `task build:cli`   | Build just the CLI binary to `bin/sandbox`          |
| `task build:image` | Build just the Docker image                        |
| `task lint`        | Run `go vet`                                       |
| `task test`        | Run unit tests (fast)                              |
| `task test:integration` | Run integration tests (builds real image, ~15 min) |
| `task install`     | Lint + build + test + integration test, then install to `~/bin` |
| `task clean`       | Remove `bin/` build artifacts                      |
