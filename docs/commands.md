# Command reference

```
dotsync <command> [options] [arguments]
```

Flags can go before or after arguments, and both `-flag` and `--flag` work. Every command accepts `-h`.

Commands that change the manifest (`add`, `remove`, `describe`) and `resolve` run a sync straight away, so the change is pushed and you see the result.

Exit status: `0` on success; `1` if something failed or an entry or file had an error; `2` on usage errors. Conflicts and blocked secrets are reported, but on their own they don't cause a non-zero exit.

---

## `dotsync init <git-url>`

Sets up this machine: clones the repository, initializes it if empty, writes the machine config, installs the binary to `~/.local/bin`, runs the first sync, and installs the background agent. Safe to run again.

| flag | description |
|---|---|
| `--keep-existing` | report existing differing local files as conflicts, instead of backing them up and replacing them |
| `--branch <name>` | branch to sync (default `main`) |
| `--interval <seconds>` | background sync interval (default `300`) |
| `--no-agent` | don't install the background agent |
| `--no-install` | don't copy the binary to `~/.local/bin` |

## `dotsync add <path>...`

Starts managing files or directories on every machine.

| flag | description |
|---|---|
| `-d`, `--description <text>` | description (single path only; defaults to the file name) |
| `--source <path>` | location under `files/` (single path only). The default is derived from the path: `~/.config/fish/config.fish` becomes `fish/config.fish`, and `~/.gitconfig` becomes `gitconfig` |
| `--ignore <glob>` | for directories: pattern to skip; repeatable |
| `--os darwin\|linux` | only manage on this OS; repeatable |
| `--allow-secrets` | allow content that looks like credentials (see [Secrets](secrets.md)) |
| `--no-sync` | only queue the change; the next sync pushes it |

Paths must be inside `$HOME`. `add` refuses files that look like secrets. For directories, it tells you how many files inside will be held back.

## `dotsync remove <path|source>...`

Stops managing entries on **every** machine. Local copies are left in place everywhere and become ordinary unmanaged files. The content stays in the repository history. Alias: `rm`.

| flag | description |
|---|---|
| `--no-sync` | only queue the change |

## `dotsync describe <path|source> <text>`

Changes an entry's description.

## `dotsync sync`

Synchronizes now. The background agent runs `dotsync sync -q` on a schedule.

| flag | description |
|---|---|
| `-q`, `--quiet` | no output; if a sync is already running, exit 0 immediately; post a desktop notification for new conflicts |

Output lines:

| label | meaning |
|---|---|
| `updated` | a remote change was installed here (the previous version was backed up) |
| `removed` | deleted on another machine and removed here (backed up) |
| `sent` | a local change was pushed |
| `deleted` | a local deletion was pushed |
| `waiting` | a local change is committed but the remote is unreachable |
| `CONFLICT` | changed here and elsewhere; nothing touched |
| `BLOCKED` | a local change looks like a secret and was not sent |
| `ERROR` | this file or entry could not be processed; the others were |

## `dotsync status`

Shows every managed entry and its state on this machine: `ok`, `N incoming`, `N outgoing`, `CONFLICT`, `BLOCKED`, `ERROR`, or `skipped` (for another OS, or excluded here). It also shows the last sync result, the agent state, queued operations, and conflicts. Alias: `st`.

| flag | description |
|---|---|
| `-v` | list every file, not just those needing attention |
| `--fetch` | fetch from the remote first. Without it, `status` compares against the last fetched state and works offline |
| `--json` | machine-readable output |

## `dotsync list`

Prints the shared manifest: every entry, including those skipped on this machine. Alias: `ls`.

| flag | description |
|---|---|
| `--json` | machine-readable output |

## `dotsync diff [path...]`

Shows a unified diff (remote → local) for files that differ, or only for the given paths.

| flag | description |
|---|---|
| `--fetch` | fetch from the remote first |

## `dotsync resolve <path>... --keep local|remote`

Settles [conflicts](conflicts.md). `--keep local` pushes this machine's version. `--keep remote` backs up the local file and installs the remote version. A directory path resolves every conflict under it.

## `dotsync log`

Shows recent commits to the shared repository. Each commit names the machine that made it.

| flag | description |
|---|---|
| `-n <count>` | number of commits (default 20) |

## `dotsync agent install|uninstall|status`

Manages the [background agent](agent.md). Run `install` again after changing `interval`.

## `dotsync version`

Prints the version, commit and build date.

## Environment

| variable | effect |
|---|---|
| `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME` | where dotsync keeps its own config, cache clone and state |
| `DOTSYNC_HOSTNAME` | machine name used in commit messages (default: short hostname) |
| `DOTSYNC_NO_NOTIFY` | set to disable desktop notifications |
| `GIT_SSH_COMMAND` | used as-is if set; otherwise dotsync runs ssh with `BatchMode=yes` so it never prompts |
