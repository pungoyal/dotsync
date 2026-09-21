<p align="center">
  <img src="assets/brand/icon.svg" width="96" height="96" alt="dotsync icon">
</p>

<h1 align="center">dotsync</h1>

<p align="center">
  <strong>Set-and-forget dotfile sync for macOS and Linux.</strong><br>
  Decide what's managed from any machine; every machine converges automatically.<br>
  Never silently overwrites a local change.
</p>

<p align="center">
  <a href="https://pungoyal.github.io/dotsync/"><strong>Documentation</strong></a> ·
  <a href="https://pungoyal.github.io/dotsync/start/quick-start/">Quick start</a> ·
  <a href="https://github.com/pungoyal/dotsync/releases/latest">Download</a>
</p>

<p align="center">
  <a href="https://github.com/pungoyal/dotsync/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/pungoyal/dotsync/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/pungoyal/dotsync/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/pungoyal/dotsync?sort=semver"></a>
  <a href="https://pkg.go.dev/github.com/pungoyal/dotsync"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/pungoyal/dotsync.svg"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/pungoyal/dotsync"><img alt="OpenSSF Scorecard" src="https://api.scorecard.dev/projects/github.com/pungoyal/dotsync/badge"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
</p>

---

```console
$ dotsync init git@github.com:you/dotfiles.git
$ dotsync add ~/.config/fish/config.fish -d "Fish shell configuration"
managing ~/.config/fish/config.fish as 'fish/config.fish'
  sent      ~/.config/fish/config.fish
dotsync: 1 sent — pushed 1c049fe

$ dotsync status
dotsync on laptop: git@github.com:you/dotfiles.git (main @ 1c049fe)
last sync: 2026-09-21T11:16:09+05:30 (ok)
automatic sync: launchd agent loaded

SOURCE            TARGET                      STATUS    DESCRIPTION
fish/config.fish  ~/.config/fish/config.fish  ok        Fish shell configuration
gitconfig         ~/.gitconfig                CONFLICT  Git identity and aliases
nvim              ~/.config/nvim/             ok        Neovim
```

That's the whole workflow. From then on a background agent keeps every machine in sync. You only hear from dotsync when two machines changed the same file and it needs you to pick a version.

## Why dotsync?

Most dotfile managers are **deployment tools**: you edit a repo, then run a command on each machine. dotsync is a **synchronizer**: edit the real file wherever you are, and it shows up everywhere else.

- **Automatic, both ways.** Edit `~/.gitconfig` on any machine and it propagates. Add or remove managed files from any machine too.
- **Safe by default.** Local files are backed up before they are replaced or deleted. If two machines change the same file, both versions are kept and you get a conflict instead of a silent winner.
- **Secrets stay home.** SSH keys, cloud credentials, tokens in shell rc files and similar things are refused before they reach the remote.
- **Offline-tolerant.** Machines that were away for a month catch up correctly when they reconnect.
- **Few moving parts.** One static binary plus `git`, and a private git repo you already know how to host. No daemon, database, server or account.
- **Light on your battery.** A sync with nothing to do takes about 65 ms and writes nothing to disk. There's no randomized scheduling and no surprise background work.
- **Portable.** Targets are written as `~/…` or `$XDG_CONFIG_HOME/…`. Per-OS entries and per-machine exclusions keep machine-specific settings out of the shared config.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/pungoyal/dotsync/main/install.sh | sh
```

The script downloads the right binary for your OS and CPU and **checks its SHA-256 checksum** before installing it to `~/.local/bin`. Later, **`dotsync update`** upgrades in place with the same checks. If the [GitHub CLI](https://cli.github.com) is installed, it also **verifies the build provenance attestation**. See [verifying releases](https://pungoyal.github.io/dotsync/project/verifying-releases/).

<details>
<summary>Other ways to install</summary>

**Prebuilt binaries:** download from the [releases page](https://github.com/pungoyal/dotsync/releases). Builds exist for macOS and Linux, on amd64 and arm64.

**With Go 1.23+:**

```sh
go install github.com/pungoyal/dotsync/cmd/dotsync@latest
```

**From source:**

```sh
git clone https://github.com/pungoyal/dotsync && cd dotsync && make build   # → bin/dotsync
```

</details>

## Quick start

1. **Create an empty private repository** on GitHub, GitLab or anywhere else you can reach with git. Make sure `git clone` works for it without a password prompt (an SSH key, or a credential helper).

2. **On your first machine:**

   ```sh
   dotsync init git@github.com:you/dotfiles.git
   dotsync add ~/.gitconfig ~/.config/fish/config.fish ~/.config/nvim
   ```

3. **On every other machine**, run the same `init`. Managed files show up. If a local file already existed and differed, it is backed up first and then replaced with the shared version.

4. **That's it.** Edit files as you normally would; they sync every 5 minutes. Run `dotsync doctor` to check the setup, and `dotsync status` any time to see what's going on.

Follow the [quick start](https://pungoyal.github.io/dotsync/start/quick-start/) for a guided tour.

## How it works in 30 seconds

A private git repository holds a **manifest**: which files are managed, where they go, and what they are. It also holds their content. Every machine keeps a private record of the version it last agreed on with the remote (the *base*). Each sync compares three versions of every file:

| local vs. base | remote vs. base | what dotsync does |
|---|---|---|
| same | same | nothing |
| same | changed | back up the local file, install the remote version |
| changed | same | push the local version |
| changed | changed | **conflict**: touch nothing, tell you |

There are no git merges, rebases or clever heuristics. The full design is in [How sync works](https://pungoyal.github.io/dotsync/concepts/how-sync-works/).

## Documentation

**📖 [pungoyal.github.io/dotsync](https://pungoyal.github.io/dotsync/)**

| | |
|---|---|
| [Quick start](https://pungoyal.github.io/dotsync/start/quick-start/) | Two machines in sync in five minutes |
| [Guides](https://pungoyal.github.io/dotsync/guides/manage-files/) | Managing files, conflicts, secrets, per-machine differences, migration |
| [How sync works](https://pungoyal.github.io/dotsync/concepts/how-sync-works/) | The model and its [safety guarantees](https://pungoyal.github.io/dotsync/concepts/safety/) |
| [Reference](https://pungoyal.github.io/dotsync/reference/commands/) | Commands, manifest, configuration, files, secret rules |

## Comparison

| | dotsync | chezmoi | yadm | GNU Stow | Mackup |
|---|---|---|---|---|---|
| Edit real files in place | ✅ | via `chezmoi edit`/`re-add` | ✅ | ✅ (symlinks) | ✅ (symlinks) |
| Syncs automatically, both ways | ✅ | ❌ (manual apply) | ❌ (manual git) | ❌ | via cloud folder |
| Manage files from any machine | ✅ | ✅ | ✅ | ❌ | ✅ |
| Conflict detection per file | ✅ | ❌ (git merge) | ❌ (git merge) | ❌ | ❌ |
| Blocks secrets from syncing | ✅ | encryption / password managers | encryption | ❌ | ❌ |
| Templates per machine | ❌ | ✅ | ✅ (alternates) | ❌ | ❌ |

dotsync deliberately does **not** do templating. If you need one file to differ per machine in complicated ways, chezmoi is excellent. dotsync is for people who want their files to simply be the same everywhere, with no ceremony.

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first. Security issues go through [SECURITY.md](SECURITY.md), not public issues.

## License

[MIT](LICENSE)
