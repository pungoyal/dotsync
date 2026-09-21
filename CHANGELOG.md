# Changelog

## [0.1.1](https://github.com/pungoyal/dotsync/compare/v0.1.0...v0.1.1) (2026-09-21)


### Bug Fixes

* **globs:** non-ASCII characters in `ignore` and secret patterns now match correctly (e.g. `café*`), and a pattern with invalid UTF-8 no longer crashes dotsync ([91cd906](https://github.com/pungoyal/dotsync/commit/91cd906))
* **manifest:** reject `source` path components with leading or trailing spaces, which normalised inconsistently ([91cd906](https://github.com/pungoyal/dotsync/commit/91cd906))

### Documentation

* add documentation site on GitHub Pages ([5ae08f2](https://github.com/pungoyal/dotsync/commit/5ae08f2487ff14bdb8a579e6145a97cd86861075))

## 0.1.0 (2026-09-21)


### Features

* initial release of dotsync ([a031097](https://github.com/pungoyal/dotsync/commit/a0310974694340871c6810181e15a0356366a17a))
