# FAQ & troubleshooting

### Can I edit the repository directly instead of using `dotsync add`?

Yes. Edit `manifest.json` and `files/` in any clone and push. Machines pick the changes up on their next sync. Keep `manifest.json` valid JSON: if it isn't, machines refuse to sync until it's fixed, and they touch nothing in the meantime. Don't edit the cache clone in `~/.local/share/dotsync/repo`. dotsync resets it on every sync.

### How do I get an old version of a file back?

- dotsync replaced or deleted it on this machine: look in `~/.local/state/dotsync/backups/<timestamp>/`. Paths inside mirror your home directory.
- Any earlier synced version: it's in the repository history (`dotsync log`, or `git log -p -- files/<source>` in a clone).

To restore one, copy it into place. The next sync sends it everywhere.

### How do I stop managing a file on just one machine?

Add its `source` to `exclude` in `~/.config/dotsync/config.json`. The file stays as it is on that machine and stops syncing there. Other machines are unaffected.

### A file should differ between macOS and Linux.

Add two entries with different sources, each limited with `os`:

```sh
dotsync add ~/.config/alacritty/alacritty.toml --source alacritty/macos.toml --os darwin   # on the Mac
dotsync add ~/.config/alacritty/alacritty.toml --source alacritty/linux.toml --os linux    # on Linux
```

A cleaner option for many tools is a shared file that includes an unmanaged, machine-local one (for example `[include] path = ~/.gitconfig.local` in git).

### `status` says `offline`.

dotsync couldn't reach the remote. The last line of git's error is shown next to it. Usually the network is down, or git needs a password it can't ask for (see [Background agent](agent.md#things-that-commonly-trip-up-background-jobs)). Local changes wait and are sent once the remote is reachable.

### `status` says `BLOCKED`.

The file now contains something that looks like a credential. See [Secrets](secrets.md).

### Some app keeps "undoing" my synced config.

Some applications keep their settings in memory and write them back when they quit. If that happens, dotsync sees a local change and syncs it back. Quit the application before large changes, or check whether it has a proper config file separate from its state.

### Why not symlinks?

See [How it works → Files are copied, not symlinked](how-it-works.md#files-are-copied-not-symlinked).

### Can I use one repository for multiple users or teams?

It's designed for one person's machines. The manifest is shared by every machine that syncs with it.

### How do I move to a different remote?

Push the repository to the new location. On each machine, run `git -C ~/.local/share/dotsync/repo remote set-url origin <new-url>` and update `remote` in `~/.config/dotsync/config.json`.

### How do I uninstall?

```sh
dotsync agent uninstall
rm -rf ~/.local/share/dotsync ~/.local/state/dotsync ~/.config/dotsync ~/.local/bin/dotsync
```

Your dotfiles are ordinary files and stay exactly as they are.
