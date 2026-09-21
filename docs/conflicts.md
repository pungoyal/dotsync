# Conflicts

A conflict means **the same file changed on this machine and on another machine** since they last agreed. dotsync never picks a winner for you. It changes nothing on this machine and nothing in the repository. It reports the conflict and waits.

## When conflicts happen

- You edited `~/.zshrc` on your laptop while it was offline, and edited it on your desktop too.
- Two machines edited the same file within the same sync interval.
- A file became managed on a machine that already had a different version, and you asked for conflicts with `init --keep-existing` (or `"on_existing": "conflict"`). Without that setting, the local file is backed up and replaced instead.

Deleting a file on one machine while it changed on another is **not** a conflict. The changed version wins, and nothing is lost.

## Seeing them

```console
$ dotsync status
…
gitconfig  ~/.gitconfig  CONFLICT  Git identity and aliases

conflicts (neither version has been changed):
  ~/.gitconfig  since 2026-09-21T11:16:09+05:30
```

The background agent also posts a desktop notification the first time it sees a conflict.

```console
$ dotsync diff ~/.gitconfig
=== ~/.gitconfig  [conflict]
--- remote/gitconfig
+++ local/gitconfig
@@ -1,3 +1,3 @@
 [user]
-	email = me@work.example
+	email = me@home.example
```

A copy of the remote version is also kept at `~/.local/state/dotsync/conflicts/<source>` while the conflict lasts.

## Resolving them

| you want | do |
|---|---|
| this machine's version everywhere | `dotsync resolve ~/.gitconfig --keep local` |
| the other version here | `dotsync resolve ~/.gitconfig --keep remote` (your version is backed up first) |
| a merge of both | edit the local file until it has what you want, then `--keep local` |
| to leave it for now | nothing; the rest of your files keep syncing |

If you make the local file identical to the remote version, the conflict clears by itself on the next sync.

`resolve` accepts a directory to settle every conflict under it, e.g. `dotsync resolve ~/.config/nvim --keep local`.

### How `resolve` works

Resolving doesn't copy anything by itself. It moves the file's *base*:

- `--keep local` records the remote version as the base. The next sync then sees "only this machine changed" and pushes your version.
- `--keep remote` records your version as the base. The next sync then sees "only the remote changed" and installs the remote version, after a backup.

The ordinary sync rules then do the work, with all their safety checks. If yet another machine changed the file in the meantime, you simply get a new conflict. No change is ever lost.
