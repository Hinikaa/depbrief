# depbrief

`depbrief` reads project lockfiles (or diffs two lockfiles), resolves updated dependencies to their upstream GitHub repositories, fetches intermediate GitHub Releases and `CHANGELOG.md` sections between the locked and target versions, and prints a consolidated breaking-change and security upgrade brief. Built with the Go 1.22+ standard library with zero external dependencies.

## Install

```sh
go install github.com/Hinikaa/depbrief/cmd/depbrief@latest
```

Or build from source:

```sh
go build -o depbrief ./cmd/depbrief
```

## Usage

Scan a directory for outdated dependencies:

```sh
depbrief scan .
depbrief scan ./services/api --format markdown --fail-on security
```

Diff two lockfiles directly:

```sh
depbrief diff old/package-lock.json new/package-lock.json
depbrief diff Cargo.lock.old Cargo.lock.new --no-changelog --format json
```

Set `GITHUB_TOKEN` in the environment to raise GitHub API rate limits when fetching releases across many packages.

## Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--format` | `text` | Output format: `text`, `markdown`, or `json` |
| `--direct-only` | `true` | Limit to direct dependencies when a manifest or direct marker exists |
| `--major-only` | `false` | Report major version upgrades only |
| `--concurrency` | `6` | Maximum concurrent upstream HTTP requests |
| `--fail-on` | `none` | Exit with code `1` on findings: `breaking`, `security`, or `none` |
| `--no-changelog` | `false` | Version comparison only; skip GitHub release and changelog requests |
| `--ecosystem` | `auto` | Override lockfile detection: `auto`, `npm`, `pnpm`, `cargo`, `uv`, or `go` |

Exit codes: `0` clean, `1` `--fail-on` condition triggered, `2` error.

## Supported Ecosystems and Lockfiles

| Ecosystem | Lockfile | Direct Dependency Detection | Registry |
| --- | --- | --- | --- |
| `npm` | `package-lock.json` (v2/v3) | `package.json` or root `packages[""]` | `registry.npmjs.org` |
| `pnpm` | `pnpm-lock.yaml` (v6/v9) | `importers` or `package.json` | `registry.npmjs.org` |
| `cargo` | `Cargo.lock` | `Cargo.toml` | `crates.io` |
| `uv` | `uv.lock` | `pyproject.toml` or local `[[package]]` | `pypi.org` |
| `go` | `go.mod` | `// indirect` comments | `proxy.golang.org` |

## License

MIT. See [LICENSE](LICENSE).
