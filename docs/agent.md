# Background agent

`dotsync init` installs a small scheduled job that runs `dotsync sync -q` every 5 minutes. No process stays running between syncs.

| platform | mechanism | files |
|---|---|---|
| macOS | launchd user agent `io.github.dotsync` | `~/Library/LaunchAgents/io.github.dotsync.plist` |
| Linux with systemd | user timer `dotsync.timer` + `dotsync.service` | `~/.config/systemd/user/` |
| Linux without systemd | a crontab line tagged `# dotsync-agent` | your user crontab |

The job runs with the same `HOME` and `XDG_*` base directories as the shell you ran `init` (or `agent install`) from. They are written into the job definition, because launchd and cron don't read your shell configuration. If you change those variables later, run `dotsync agent install` again.

The agent also runs at login (macOS) or one minute after boot (systemd). The systemd timer is `Persistent=`, so a run missed while the machine was asleep happens on wake.

```sh
dotsync agent status      # is it installed and loaded?
dotsync agent install     # (re)install, e.g. after changing "interval" in config.json
dotsync agent uninstall
```

## Logs

| file | contents |
|---|---|
| `~/.local/state/dotsync/sync.log` | one line per change, conflict and error, plus a summary per sync (rotated at 1 MiB) |
| `~/.local/state/dotsync/agent.log` | anything the scheduled job printed to stdout or stderr |

`dotsync status` shows the result of the last sync, including why the remote was unreachable.

## Notifications

When a sync finds a **new** conflict, or git starts failing to authenticate to the remote, the agent posts a desktop notification: Notification Center on macOS, `notify-send` on Linux where it's available. Set `DOTSYNC_NO_NOTIFY=1` to turn this off.

## Things that commonly trip up background jobs

- **SSH keys with passphrases.** The agent can't type a passphrase. On macOS, launchd agents can use keys loaded into the system `ssh-agent`, including via Keychain (`UseKeychain yes`). On Linux, systemd user services generally can't see your desktop session's `SSH_AUTH_SOCK`. Use a dedicated deploy key without a passphrase, or an HTTPS credential helper. `dotsync status` shows the exact git error.
- **`git` not found.** The agent runs with a minimal `PATH`: the directory `git` was found in at install time, plus `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin` and `/bin`. If you move git, run `dotsync agent install` again.
- **Moved binary.** The agent runs `~/.local/bin/dotsync`. If you installed elsewhere and later move it, reinstall the agent.
- **systemd user services on headless servers.** Without lingering, user timers only run while you're logged in. Enable lingering with `loginctl enable-linger $USER`.
