# Secrets

dotsync's approach to secrets is simple: **they never leave the machine.** Secrets are not encrypted into the repository. They are kept out of it. When a file looks like it contains credentials, it isn't uploaded until you explicitly say it's fine.

## What is blocked

**By path** (case-insensitive). Patterns containing `/` are matched against the path relative to `$HOME` (`*` matches across directories). The others are matched against the file name only, so `*secret*` blocks `my-secrets.json` but not files inside a directory called `~/.config/secretive/`.

| pattern | examples |
|---|---|
| `.ssh/id_*`, `.ssh/*_key`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore` | SSH and TLS private keys |
| `*.kdbx`, `.password-store/*`, `.gnupg/private-keys-v1.d/*`, `.gnupg/secring.*`, `.gnupg/*.key`, `.local/share/keyrings/*` | password stores, private GnuPG keys and keyrings (`gpg.conf` and `gpg-agent.conf` sync fine) |
| `.aws/credentials`, `.aws/sso/*`, `.azure/*`, `.config/gcloud/*`, `.kube/config` | cloud credentials |
| `.docker/config.json`, `.netrc`, `.git-credentials`, `.pgpass`, `.vault-token`, `.config/gh/hosts.yml` | tool credentials |
| `.env`, `.env.*`, `*.env`, `*secret*`, `*credential*` | environment and secret files |
| `.*history` | shell and REPL histories |

**By content**, line by line:

- private key blocks (`-----BEGIN … PRIVATE KEY-----`)
- AWS access key IDs (`AKIA…`, `ASIA…`)
- GitHub (`ghp_…`, `github_pat_…`), GitLab (`glpat-…`), Slack (`xox?-…`) and Google API (`AIza…`) tokens
- `sk-…`-style API keys (OpenAI, Anthropic and others)
- npm `_authToken=…`
- assignments such as `password = …`, `token: …`, `api_key: …`, `client_secret=…` whose value is a literal of 8 or more characters. Values that start with `$` or `$(`, i.e. read from a variable or a command, are fine

## What happens

- **`dotsync add`** refuses a file that matches and explains why:

  ```
  dotsync: ~/.aws/config: refusing to manage it: content looks like a AWS access key
  ```

- **A managed file that later gains a secret** is marked **BLOCKED**. Its new version is not uploaded, and `sync` and `status` report it. The previous version in the repository is unaffected. Once you remove the secret, syncing resumes.
- **Inside a managed directory**, files matching the path rules are never uploaded. `add` tells you how many were held back. Add them to `ignore` to silence the report.

## Keeping secrets out of dotfiles

The cleanest fix is usually to move the secret out of the synced file:

```sh
# ~/.config/fish/config.fish (synced)
test -f ~/.config/fish/secrets.fish; and source ~/.config/fish/secrets.fish

# ~/.config/fish/secrets.fish (not managed, stays local)
set -gx GITHUB_TOKEN ghp_…
```

Or fetch secrets at runtime from a password manager or the OS keychain (`security find-generic-password`, `secret-tool`, `op read`, `pass`).

## False positives

- Put `dotsync:allow-secret` anywhere on the offending line, typically in a comment:

  ```ini
  password_command = pass show mail  # dotsync:allow-secret
  ```

- Or opt a whole entry out with `dotsync add --allow-secrets`, or `"allow_secrets": true` in the manifest. Only do this if you are sure the repository is an acceptable place for its contents.

## Limits

Pattern matching reduces risk; it can't guarantee anything. Always keep the repository **private**. If a secret does reach the repository, rotate it: removing it from the latest commit doesn't remove it from the history.
