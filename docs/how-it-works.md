# How it works

dotsync is built around one idea: **for every managed file, remember the version this machine last agreed on with everyone else.** With that one fact, every situation has an obvious and safe answer.

## The pieces

```
                ┌────────────────────────────────────────┐
                │  shared private git repository         │
                │  ├── manifest.json   what is managed   │  ◀── single source of truth
                │  └── files/…         the content       │
                └────────────────────────────────────────┘
                          ▲ push              │ fetch
                          │ (one-sided        ▼
                          │  local changes)
┌─────────────────────────┴──────────────────────────────────────────┐
│ each machine                                                       │
│                                                                    │
│  your real files         ~/.gitconfig, ~/.config/nvim/…            │
│  cache clone             ~/.local/share/dotsync/repo   (disposable)│
│  state                   ~/.local/state/dotsync/state.json         │
│                          · base signature of every managed file    │
│                          · queued add/remove/describe operations   │
│                          · current conflicts, last sync result     │
│  backups                 ~/.local/state/dotsync/backups/…          │
│  machine config          ~/.config/dotsync/config.json             │
└────────────────────────────────────────────────────────────────────┘
```

Only the repository is shared. Everything else is private to the machine and never synced.

## One sync, step by step

1. **Lock.** Take an exclusive lock (`flock`) so the agent and a manual `dotsync sync` never run at the same time.
2. **Fetch.** `git fetch` the remote branch. If the remote is unreachable, carry on with the last copy fetched; remote changes already fetched are still applied.
3. **Reset the cache clone** to exactly the remote commit. The clone never holds anything that isn't on the remote, so it can never diverge.
4. **Replay queued operations.** `dotsync add`, `remove` and `describe` don't edit git directly. They queue an operation in local state, and each sync re-applies the queue on top of the *current* remote manifest. That is how two machines can add different files at the same moment without either change getting lost.
5. **Plan.** For every managed file, compute three signatures (a hash of type, executable bit and content):
   - **L**: the file on this machine
   - **R**: the file in the remote repository
   - **B**: the *base*, i.e. what this machine last agreed on with the remote

   and decide:

   | condition | action |
   |---|---|
   | L = R | nothing (record R as the new base) |
   | L = B, R ≠ B | the remote changed: **back up** the local file, install R |
   | R = B, L ≠ B | this machine changed: stage L for pushing |
   | L ≠ B, R ≠ B, L ≠ R | both changed: **conflict**. Nothing is touched |
   | no base, L ≠ R, entry never synced on this machine | newly managed here: back up the local file, install R (or a conflict with `on_existing: "conflict"`) |
   | no base, L ≠ R, entry already synced here | e.g. the same new file created on two machines: **conflict** |
   | L missing, R changed | a modification beats a deletion: install R |
   | R missing, L changed | a modification beats a deletion: push L |

6. **Apply** remote changes locally. Just before replacing a file, dotsync re-reads it. If it changed since planning, the file is skipped and retried next time.
7. **Commit and push** local changes and the updated manifest in a single commit.
8. **If the push is rejected** because another machine pushed first, go back to step 2 and redo the whole pass on top of the new remote commit. There are no merges and no rebases, so there are no git conflicts.
9. **Save state** atomically. The base of a pushed file only advances **after the push succeeds**.

## Guarantees

**Nothing local is overwritten without a copy.** Every file dotsync replaces or deletes is first copied to `~/.local/state/dotsync/backups/<timestamp>/<path relative to $HOME>`. Before replacing a file, dotsync checks that it still has the content it planned against.

**Concurrent edits are never merged or discarded.** If the same file changed on two machines, it becomes a conflict. Each machine keeps its own version until you [resolve it](conflicts.md).

**Convergence is deterministic.** The remote is a linear history, and every machine applies the same rules to the same manifest. Once machines have synced they hold identical managed files, except for explicitly reported conflicts, blocked secrets and per-machine exclusions.

**Offline is normal.** Your edits live in your files, and queued manifest operations live in `state.json`. Neither depends on git working. A machine that reconnects after a month runs the same comparison and ends up in the right state, including conflicts if the same file was edited in the meantime.

**Crashes are harmless.** Local files are written atomically (temp file, `fsync`, `rename`). The cache clone is reset at the start of every sync. The base only moves after a successful write or push. If a sync is killed at any point, the next one simply does the remaining work.

**Everything is recoverable.** The remote has the full history (`dotsync log`, or any git tool), and every local replacement has a backup.

## Entries: files and directories

A **file entry** maps one file. If the target is missing locally, it is restored. It is never treated as deleted everywhere. To stop managing it, use `dotsync remove`.

A **directory entry** maps a directory recursively. Files added, changed or deleted inside it propagate. These safety rules apply:

- If the *whole directory* is missing or empty on a machine, it is restored, not deleted everywhere. So `rm -rf ~/.config/nvim` (or an unmounted volume) on one laptop doesn't wipe it on the others.
- dotsync never reads or writes through a symlinked or replaced *parent* directory inside a managed tree. If `~/.config/app/sub` turns into a symlink, files below it are reported as errors instead of being followed out of the tree or deleted everywhere.
- Deleted files are backed up on every machine that removes them.

Inside directories, symlinks are synced as symlinks. The executable bit is synced. Other permissions stay as they are on each machine. Empty directories are not tracked, same as in git.

**Removing** an entry (`dotsync remove`) stops managing it everywhere. The files stay in place on every machine as ordinary unmanaged files, and later edits stay local.

## Files are copied, not symlinked

Many dotfile tools symlink files into a repository. dotsync copies them:

- Many applications save their config by writing a new file and renaming it over the old one. That silently replaces a symlink with a regular file.
- Copies let dotsync tell "you edited it" apart from "the repository changed", which is the basis of conflict detection.
- Your home directory stays a normal home directory. If you uninstall dotsync, everything keeps working.

If a target is *already* a symlink pointing inside your home directory (from an earlier stow or yadm setup, for example), dotsync follows it and updates the real file.

For applications that watch a file's inode or bind-mount a single file (some containers do), set `"write": "inplace"` on the entry. dotsync then rewrites the existing file in place instead of renaming a new one over it.

## Machine-specific configuration

The manifest is shared, so it must not contain anything machine-specific:

- Targets must be `~/…` or `$VAR/…`. `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME` and `XDG_CACHE_HOME` fall back to their standard defaults. Absolute paths are rejected, and so is anything that resolves outside `$HOME`.
- `"os": ["darwin"]` limits an entry to one operating system.
- Per-machine opt-outs live in `~/.config/dotsync/config.json` (`exclude`), which is never synced.

## Failure handling

| failure | behavior |
|---|---|
| network or remote down | local changes wait; already fetched remote changes are applied; `status` shows `offline` |
| push rejected (race) | whole pass redone on top of the new remote, up to 5 times |
| invalid `manifest.json` on the remote | sync stops before touching any file and reports the parse error |
| one bad entry (bad target, type mismatch…) | that entry is skipped and reported; the others sync normally |
| a file changes while being synced | that file is skipped and retried next time |
| local state file corrupted | moved aside; on the next sync every file that differs from the remote becomes a conflict rather than being replaced |
| authentication to the remote fails | reported as `offline` with git's error; the agent sends a desktop notification, because this doesn't fix itself |
| two dotsync processes | the second waits (interactive) or exits quietly (agent) |

## What dotsync does not do

- **Templating.** Files are identical on every machine where they are managed. Use per-OS entries or per-machine exclusions for variation, or a tool like chezmoi if you need real templates.
- **Encryption.** Secrets are kept *out* of the repository rather than encrypted into it. See [Secrets](secrets.md).
- **Real-time sync.** Changes propagate on the agent's interval (5 minutes by default).
- **Large or binary-heavy directories.** Files over 10 MiB are refused. Dotfiles are small, and git is not a backup tool for media.
