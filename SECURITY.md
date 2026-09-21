# Security policy

dotsync handles personal configuration files and runs unattended, so we take security reports seriously.

## Supported versions

Security fixes are made for the latest release. Please upgrade before reporting.

## Signing keys

Release files are signed through Sigstore with the release workflow's own identity; there's no long-lived key (see [verifying releases](https://pungoyal.github.io/dotsync/project/verifying-releases/)). The only long-lived key signs the APT repository's index:

| Key | Fingerprint |
|---|---|
| dotsync APT repository <pungoyal@gmail.com> (RSA 4096) | `FD8B DCCB 9FAE 77A7 03F3  7154 3EC2 40F3 0AB4 EE33` |

It's served at https://pungoyal.github.io/dotsync/apt/dotsync.asc. If it ever changes, this file and a release note will say so.

## Reporting a vulnerability

**Please do not open a public issue.** Report privately through GitHub's [private vulnerability reporting](https://github.com/pungoyal/dotsync/security/advisories/new).

Please include what you found, how to reproduce it, and the impact you expect. You should get an acknowledgement within 3 working days. We aim to ship a fix, and publish an advisory crediting you (unless you'd rather not be credited), within 30 days.

## In scope

- anything that makes dotsync write, delete or replace files outside the managed targets, or outside `$HOME`
- losing a local change without a backup, or resolving a conflict silently
- secrets reaching the remote despite the [secret filters](https://pungoyal.github.io/dotsync/reference/secret-rules/) in a way the documentation says is prevented. Improvements to the patterns themselves are welcome as regular issues
- command injection through manifest content, file names or configuration
- problems in the release pipeline or the install script

## Out of scope

- someone with write access to your dotfiles repository changing what your machines receive. That is the trust model: whoever controls the repository controls your dotfiles. Keep it private and protect access to it
- secrets that don't match any documented pattern
