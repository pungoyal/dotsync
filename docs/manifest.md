# Manifest reference

The manifest (`manifest.json` at the root of the shared repository) is the authoritative list of managed files. Usually you change it with `dotsync add`, `remove` and `describe`. You can also edit it by hand in any clone of the repository. dotsync validates it on every sync.

## Example

```json
{
  "version": 1,
  "entries": [
    {
      "source": "fish/config.fish",
      "target": "~/.config/fish/config.fish",
      "description": "Fish shell configuration",
      "type": "file"
    },
    {
      "source": "nvim",
      "target": "$XDG_CONFIG_HOME/nvim",
      "description": "Neovim",
      "type": "dir",
      "ignore": ["lazy-lock.json", "spell/*.spl"]
    },
    {
      "source": "karabiner/karabiner.json",
      "target": "~/.config/karabiner/karabiner.json",
      "description": "Keyboard remapping",
      "os": ["darwin"],
      "write": "inplace"
    },
    {
      "source": "ssh/config",
      "target": "~/.ssh/config",
      "description": "SSH client configuration",
      "mode": "0600"
    }
  ]
}
```

## Entry fields

| field | required | description |
|---|---|---|
| `source` | yes | Path of the content under `files/` in the repository. Relative, with no `..` or `.git` components. Two sources may not overlap (one inside the other), compared case-insensitively because macOS file systems usually are. |
| `target` | yes | Where the file or directory lives on each machine. See [targets](#targets). |
| `description` | yes | Short, human-readable description shown by `status` and `list`. |
| `type` | no | `"file"` or `"dir"`. `dotsync add` always sets it. If it's missing, dotsync infers it from the repository, then from the local target. |
| `os` | no | List of operating systems that manage this entry: `"darwin"`, `"linux"`. Other machines skip it. |
| `mode` | no | Octal permissions forced on every machine, e.g. `"0600"`. Without it, a file keeps its local permissions and only the executable bit is synced. |
| `write` | no | `"atomic"` (default) writes a temporary file and renames it into place. `"inplace"` rewrites the existing file, which keeps its inode; use it for applications that watch the inode or bind-mount the file. |
| `ignore` | no | Glob patterns (directory entries) for files that should never sync. Each pattern is matched against both the file name and the path relative to the entry; `*` also matches `/`. A pattern matching a directory skips everything below it. |
| `allow_secrets` | no | `true` turns off the [secret filters](secrets.md) for this entry. |

Fields dotsync doesn't know about are preserved, so newer versions can add fields without older ones dropping them.

These patterns are always ignored inside directories: `.DS_Store`, `._*`, `*.swp`, `*.swo`, `*~`, `.#*`, `.git`, `__pycache__`, `*.pyc`.

## Targets

Targets must be portable, meaning the same text works on every machine:

| form | expands to |
|---|---|
| `~/…` | `$HOME/…` |
| `$XDG_CONFIG_HOME/…` | `$XDG_CONFIG_HOME`, or `~/.config` if unset |
| `$XDG_DATA_HOME/…` | `$XDG_DATA_HOME`, or `~/.local/share` if unset |
| `$XDG_STATE_HOME/…` | `$XDG_STATE_HOME`, or `~/.local/state` if unset |
| `$XDG_CACHE_HOME/…` | `$XDG_CACHE_HOME`, or `~/.cache` if unset |
| `$ANY_VAR/…` / `${ANY_VAR}/…` | the variable's value (must be an absolute path); entries using an unset variable report an error on that machine |

A target must resolve **inside `$HOME`** and outside dotsync's own directories. Absolute paths are rejected because they're machine-specific. Two targets may not overlap.

## Validation

On every sync, each entry is validated:

- If `manifest.json` itself isn't valid JSON, the sync **stops before touching anything** and reports the error.
- An invalid entry is skipped and reported by `sync` and `status`. All other entries sync normally, and the invalid entry's history on this machine is preserved.

## Machine configuration

`~/.config/dotsync/config.json` holds settings for one machine only. It is never synced. `init` creates it.

```json
{
  "remote": "git@github.com:you/dotfiles.git",
  "branch": "main",
  "interval": 300,
  "exclude": ["karabiner/karabiner.json"],
  "on_existing": "remote"
}
```

| field | description |
|---|---|
| `remote` | URL of the shared repository |
| `branch` | branch to sync (default `main`) |
| `interval` | seconds between background syncs (default `300`). Run `dotsync agent install` after changing it. |
| `exclude` | manifest `source`s that this machine does not manage |
| `on_existing` | what to do when a file becomes managed here while a different local file already exists: `"remote"` (default) backs it up and installs the shared version; `"conflict"` leaves it alone and reports a [conflict](conflicts.md) |

`XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME` move dotsync's own files as you would expect.

## Repository layout

```
manifest.json
files/
  fish/config.fish
  nvim/init.lua
  nvim/lua/…
README.md          created by init
.gitattributes     "* -text": content is stored byte for byte
```
