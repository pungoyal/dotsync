# Getting started

This guide takes you from nothing to two machines with synchronized dotfiles, in about five minutes.

## 1. Prerequisites

- macOS or Linux
- `git` 2.20 or newer
- An **empty, private** git repository you can push to: GitHub, GitLab, Gitea, a bare repo over SSH, anything.

dotsync syncs in the background, so git must reach the repository **without prompting**. Check this before you start:

```sh
git ls-remote git@github.com:you/dotfiles.git
```

If it asks for a password or passphrase, fix that first:

- **SSH:** load your key into `ssh-agent`. On macOS, add `UseKeychain yes` and `AddKeysToAgent yes` to `~/.ssh/config`, or use a key without a passphrase that is dedicated to this repository (a GitHub *deploy key* with write access works well).
- **HTTPS:** configure a credential helper (`git config --global credential.helper osxkeychain` on macOS, or `store`/`libsecret` on Linux).

> [!IMPORTANT]
> Keep the repository **private**. dotsync blocks common secrets, but configuration files often contain things you still would rather not publish, such as hostnames, email addresses or internal URLs.

## 2. Install

```sh
curl -fsSL https://raw.githubusercontent.com/pungoyal/dotsync/main/install.sh | sh
```

This installs `dotsync` into `~/.local/bin`. If that directory isn't on your `PATH`, add it:

```sh
# bash / zsh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.profile
# fish
fish_add_path ~/.local/bin
```

## 3. Set up your first machine

```sh
dotsync init git@github.com:you/dotfiles.git
```

`init` does the following:

1. clones the repository into `~/.local/share/dotsync/repo` (a private cache; you never edit it)
2. creates `manifest.json` if the repository is empty
3. writes this machine's settings to `~/.config/dotsync/config.json`
4. copies itself to `~/.local/bin/dotsync`
5. runs the first sync
6. installs the background agent (launchd on macOS, a systemd user timer or cron on Linux)

Now tell dotsync what to manage:

```sh
dotsync add ~/.gitconfig -d "Git identity and aliases"
dotsync add ~/.config/fish/config.fish -d "Fish shell configuration"
dotsync add ~/.config/nvim -d "Neovim" --ignore lazy-lock.json
```

Each `add` records the entry in the shared manifest and pushes the content. Directories are managed recursively. Editor swap files, `.DS_Store`, `.git` and anything matching `--ignore` are skipped.

Check the result:

```sh
dotsync status
dotsync list      # the shared manifest
dotsync log       # history of the shared repository
```

## 4. Set up your second machine

Install dotsync, then run the same `init`:

```sh
dotsync init git@github.com:you/dotfiles.git
```

Every managed file is installed. If one of them already existed with different content (a distribution's default `.bashrc`, say), the old version is **backed up** to `~/.local/state/dotsync/backups/` before it is replaced.

> [!TIP]
> Would you rather review differences before anything is replaced? Use `dotsync init --keep-existing`. Differing files are then reported as [conflicts](conflicts.md) and left alone until you decide.

## 5. Daily use

Just edit your files. Every five minutes, each machine:

- sends the files you changed
- installs files changed on other machines (after a backup)
- picks up entries added or removed elsewhere

Want it now instead of within five minutes? Run `dotsync sync`.

Some common tasks:

| I want to… | Run |
|---|---|
| see what's managed and its state | `dotsync status` (add `-v` for every file) |
| manage another file | `dotsync add <path>` |
| stop managing something (everywhere) | `dotsync remove <path>`. Local copies are kept. |
| not manage an entry on *this* machine only | add its source to `exclude` in `~/.config/dotsync/config.json` |
| manage something on macOS only | `dotsync add <path> --os darwin` |
| see what differs from the remote | `dotsync diff` |
| settle a conflict | `dotsync resolve <path> --keep local` or `--keep remote` |
| undo a change | find the previous version in `dotsync log`/the repo history, or in `~/.local/state/dotsync/backups/` |

## Next steps

- [How it works](how-it-works.md): what each sync actually does, and what dotsync guarantees
- [Conflicts](conflicts.md)
- [Manifest reference](manifest.md): OS filters, permissions, write strategies
