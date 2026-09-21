# Changelog

## [0.2.0](https://github.com/pungoyal/dotsync/compare/v0.1.1...v0.2.0) (2026-09-21)


### Features

* add dotsync doctor to check the setup and explain fixes ([#3](https://github.com/pungoyal/dotsync/issues/3)) ([0daf6df](https://github.com/pungoyal/dotsync/commit/0daf6dfa65b5ae1fac4c8f9f8b3efda1ef7a00a4))
* add dotsync exclude and include to opt a single machine out of an entry ([0daf6df](https://github.com/pungoyal/dotsync/commit/0daf6dfa65b5ae1fac4c8f9f8b3efda1ef7a00a4))
* add dotsync set to change an entry's ignore patterns, OS, mode, write strategy or description without editing JSON ([0daf6df](https://github.com/pungoyal/dotsync/commit/0daf6dfa65b5ae1fac4c8f9f8b3efda1ef7a00a4))
* prune backups older than backup_retention_days (default 90), always keeping the newest copy of each file ([0daf6df](https://github.com/pungoyal/dotsync/commit/0daf6dfa65b5ae1fac4c8f9f8b3efda1ef7a00a4))

## [0.1.1](https://github.com/pungoyal/dotsync/compare/v0.1.0...v0.1.1) (2026-09-21)


### Bug Fixes

* **globs:** non-ASCII characters in `ignore` and secret patterns now match correctly (e.g. `café*`), and a pattern with invalid UTF-8 no longer crashes dotsync ([91cd906](https://github.com/pungoyal/dotsync/commit/91cd906))
* **manifest:** reject `source` path components with leading or trailing spaces, which normalised inconsistently ([91cd906](https://github.com/pungoyal/dotsync/commit/91cd906))

### Documentation

* add documentation site on GitHub Pages ([5ae08f2](https://github.com/pungoyal/dotsync/commit/5ae08f2487ff14bdb8a579e6145a97cd86861075))

## 0.1.0 (2026-09-21)


### Features

* initial release of dotsync ([a031097](https://github.com/pungoyal/dotsync/commit/a0310974694340871c6810181e15a0356366a17a))
