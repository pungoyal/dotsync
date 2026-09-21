# Contributing to dotsync

Thanks for your interest! Bug reports, fixes, docs and ideas are all welcome.

## Ground rules

dotsync touches people's configuration files, so **safety comes first**:

- A change must never make it possible to lose a local modification without a backup, or to resolve a conflict silently.
- New behavior needs tests. For sync behavior that means a multi-machine scenario test (see below).
- The public surface is kept small on purpose. Please open an issue to discuss new commands, manifest fields or config options before writing the code.

Everyone taking part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development setup

You need Go (see `go.mod` for the minimum version) and git.

```sh
git clone https://github.com/pungoyal/dotsync && cd dotsync
make test        # go vet + tests with the race detector
make lint        # golangci-lint (install: https://golangci-lint.run/welcome/install/)
make build       # bin/dotsync
```

Try your build without touching your real dotfiles by pointing `HOME` at a scratch directory:

```sh
mkdir -p /tmp/ds/{remote,a,b}
git init -q --bare --initial-branch=main /tmp/ds/remote/repo.git
HOME=/tmp/ds/a DOTSYNC_HOSTNAME=a ./bin/dotsync init --no-agent --no-install /tmp/ds/remote/repo.git
HOME=/tmp/ds/b DOTSYNC_HOSTNAME=b ./bin/dotsync init --no-agent --no-install /tmp/ds/remote/repo.git
```

## Code layout

| path | what |
|---|---|
| `cmd/dotsync` | `main`; nothing else |
| `internal/dotsync/plan.go` | the sync policy (`decide`) and planning |
| `internal/dotsync/sync.go` | executing a plan, the push/retry loop |
| `internal/dotsync/manifest.go` | manifest parsing, validation, target expansion, queued operations |
| `internal/dotsync/fsobj.go` | reading and writing files, atomically and safely |
| `internal/dotsync/secrets.go` | secret detection |
| `internal/dotsync/agent.go` | launchd / systemd / cron |
| `internal/dotsync/commands.go` | the CLI |
| `internal/dotsync/dotsync_test.go` | scenario tests: several simulated machines sharing a local bare repository |

[How sync works](https://pungoyal.github.io/dotsync/concepts/how-sync-works/) explains the model. Read it before changing `plan.go` or `sync.go`.

## Tests

Scenario tests create a `world` (a bare remote) and `machine`s (separate `$HOME`s), then drive the real CLI:

```go
w := newWorld(t)
a, b := w.machine("alpha"), w.machine("beta")
a.init()
a.write(".vimrc", "set number\n")
a.ok("add", a.path(".vimrc"))
b.init()
b.expect(".vimrc", "set number\n")
```

Please add one for every behavior change and every bug fix.

## Documentation

The documentation site (https://pungoyal.github.io/dotsync/) is built with [Astro Starlight](https://starlight.astro.build) from `website/`, and deployed to GitHub Pages on every push to `main`. Pages are Markdown/MDX under `website/src/content/docs/`, organised by purpose:

| Section | Kind of page | Write it as… |
|---|---|---|
| `start/` | tutorials | a guided path someone can follow top to bottom |
| `guides/` | how-to guides | steps for one task, assuming the reader knows what they want |
| `concepts/` | explanation | the why: models, trade-offs, guarantees |
| `reference/` | reference | exhaustive, accurate and dry; mirrors the code |

```sh
cd website
npm ci
npm run dev      # http://localhost:4321/dotsync/
npm run build    # also checks every internal link
```

Tools (Go, Node, linters, GoReleaser) are pinned in `mise.toml`: run `mise install`.

## Brand assets

The icon and mark are generated. Edit `assets/brand/generate_icon.py` (geometry and colours), then run:

```sh
cd website && npm run brand
```

That regenerates everything derived from them:

- `assets/brand/*.svg` and `assets/brand/png/`
- the social preview image
- the site's favicons, web manifest, logos and `og.png`
- the icon embedded in the binary (`internal/dotsync/assets/icon.png`)

Commit the results.

## Commits and pull requests

- We use [Conventional Commits](https://www.conventionalcommits.org/): `feat: …`, `fix: …`, `docs: …`, `refactor: …`, `test: …`, `ci: …`, `chore: …`. Use `feat!:` or a `BREAKING CHANGE:` footer for breaking changes. Release notes and version numbers are generated from these messages, and PR titles are checked.
- Keep PRs focused. CI (tests on macOS and Linux, lint, vulnerability scan) must pass.
- Update the documentation site in `website/src/content/docs/` when behavior changes (see [Documentation](#documentation)). Don't edit `CHANGELOG.md` by hand; it's generated.

## Releases

Releases are automated with [release-please](https://github.com/googleapis/release-please) and [GoReleaser](https://goreleaser.com):

1. Merging to `main` keeps a *release PR* up to date. It bumps the version and updates `CHANGELOG.md` based on the conventional commits since the last release.
2. When a maintainer merges the release PR, release-please tags the commit and creates a GitHub release.
3. The same workflow then builds binaries for macOS and Linux (amd64/arm64) with GoReleaser, uploads them with checksums and SBOMs, and creates signed build provenance attestations.

PRs opened with the workflow's `GITHUB_TOKEN` don't trigger CI on their own, so the release workflow dispatches CI and CodeQL onto the release PR's branch itself. Their results appear on the PR as usual. **Only merge a release PR once those checks have passed.** Optionally, a fine-grained token with *contents* and *pull requests* write access, stored as the `RELEASE_PLEASE_TOKEN` secret, makes release PRs behave exactly like normal ones.

No one builds or uploads release artifacts by hand. Maintainers can re-run the build for an existing tag from the *release* workflow's "Run workflow" button.
