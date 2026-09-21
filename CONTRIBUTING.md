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

### Generated reference

The command usage lines and flag tables, the secret rules, the always-ignored patterns and the defaults on the site come from `website/src/data/reference.json`, which a test generates from the code. After changing a command, flag, secret rule or default, regenerate it:

```sh
make reference   # go test ./internal/dotsync -run TestWebsiteReference -update-reference
```

`go test` fails while the file is out of date, and the site's build fails if a secret rule has no explanation in `website/src/components/SecretRules.astro`.

### Terminal output

Show dotsync's output with the `Terminal` component, copying what dotsync actually prints: run the commands, don't write the output from memory. Define the transcript with `export const` at the top of the page (MDX strips leading spaces inside component attributes), and start command lines with `$ `:

```mdx
import Terminal from '../../../components/Terminal.astro';

export const out = `
$ dotsync sync
  sent      ~/.gitconfig
dotsync: 1 sent — pushed 51c7e02
`;

<Terminal title="laptop" code={out} />
```

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
3. The same workflow then builds binaries for macOS and Linux (amd64/arm64) and `.deb` packages with GoReleaser, uploads them with checksums and SBOMs, and creates signed build provenance attestations.
4. It publishes the Homebrew formula (generated by `packaging/homebrew/formula.sh`) to the tap, and runs the docs workflow, which rebuilds the signed APT repository (`packaging/apt/build-repo.sh`) from the new release and deploys it with the site at `/apt/`.

CI builds a snapshot of every PR and tests the packages as users get them: the `.deb` is checked with lintian and installed with apt from a throwaway signed repository on Ubuntu and Debian, and the formula is installed, tested and audited with Homebrew on macOS and Linux.

### Package publishing setup

Both are optional: without them, releases still ship the `.deb` files and archives.

- **Homebrew tap.** Create the public repository `pungoyal/homebrew-tap` and set the repository variable `HOMEBREW_TAP` to `pungoyal/homebrew-tap`. The release job pushes the formula as the release GitHub App if it is installed on the tap, otherwise with `secrets.HOMEBREW_TAP_TOKEN`, a fine-grained token with *Contents: read and write* on the tap only.
- **APT repository signing key.** Generate a dedicated signing key, store it as `secrets.APT_SIGNING_KEY`, and keep an offline backup (with its revocation certificate):

  ```sh
  export GNUPGHOME=$(mktemp -d)
  gpg --batch --passphrase '' --quick-gen-key "dotsync APT repository <pungoyal@gmail.com>" rsa4096 sign never
  gpg --armor --export-secret-keys | gh secret set APT_SIGNING_KEY
  gpg --fingerprint   # publish this in SECURITY.md
  ```

  The key deliberately doesn't expire: users fetch it once, when they add the repository, so an expired key would break `apt update` on every machine until each user fetched it again. If it's ever compromised, revoke it with the revocation certificate gpg saved in `$GNUPGHOME/openpgp-revocs.d/`, and publish a new one.

Release PRs get the same checks as any other PR. **Only merge one once they have passed.** release-please opens the release PR as the repository's GitHub App (`vars.RELEASE_APP_CLIENT_ID`, `secrets.RELEASE_APP_PRIVATE_KEY`), so its checks run and show like any other PR's. Without the app, the PR is opened by `github-actions[bot]`, whose `pull_request` runs GitHub holds for approval; the release workflow then dispatches CI and CodeQL onto the release PR instead, and they report their results as commit statuses (`ci` and `codeql`), which the PR shows.

The `ci ok` job summarizes the whole CI workflow in one check. It is required for merging to `main`.

No one builds or uploads release artifacts by hand. Maintainers can re-run the build for an existing tag from the *release* workflow's "Run workflow" button.
