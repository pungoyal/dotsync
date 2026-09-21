# Changelog

## [0.5.0](https://github.com/pungoyal/dotsync/compare/v0.4.1...v0.5.0) (2026-09-21)


### Features

* dotsync help &lt;command&gt;, from a single command table ([#22](https://github.com/pungoyal/dotsync/issues/22)) ([0593cf6](https://github.com/pungoyal/dotsync/commit/0593cf66ff796b6d5788f0d456547c6ff97d982a))

## [0.4.1](https://github.com/pungoyal/dotsync/compare/v0.4.0...v0.4.1) (2026-09-21)


### Bug Fixes

* explain git failures that need the user, with the exact fix ([#17](https://github.com/pungoyal/dotsync/issues/17)) ([11cd1a7](https://github.com/pungoyal/dotsync/commit/11cd1a7a207ac5e90d3da240e15bc411883b5401))


### Performance

* faster CI (parallel fuzz, per-job build caches, fewer git processes) ([#15](https://github.com/pungoyal/dotsync/issues/15)) ([73cca51](https://github.com/pungoyal/dotsync/commit/73cca518337fdbe889e9ea8cfc48ea3543f639a2))

## [0.4.0](https://github.com/pungoyal/dotsync/compare/v0.3.0...v0.4.0) (2026-09-21)


### Features

* sync age/sops-encrypted files, block age keys, and add a mise guide ([#14](https://github.com/pungoyal/dotsync/issues/14)) ([3b8a75c](https://github.com/pungoyal/dotsync/commit/3b8a75c161d3f6fda411f2e9ea1668f2c7f9603a))


### Bug Fixes

* keep the background agent's definition current after updates ([#12](https://github.com/pungoyal/dotsync/issues/12)) ([e277a60](https://github.com/pungoyal/dotsync/commit/e277a609d62ded5d72077c8dd3522d5ab281edaa))

## [0.3.0](https://github.com/pungoyal/dotsync/compare/v0.2.0...v0.3.0) (2026-09-21)


### Features

* add dotsync update to install the latest verified release ([#5](https://github.com/pungoyal/dotsync/issues/5)) ([c707422](https://github.com/pungoyal/dotsync/commit/c707422ddcff3eef071829a2fe477a11081019d3))
* add the dotsync icon and use it everywhere ([#7](https://github.com/pungoyal/dotsync/issues/7)) ([43132a1](https://github.com/pungoyal/dotsync/commit/43132a1885ee1d696c485af90381bb175b1ef7fb))


### Bug Fixes

* **install:** show progress, abort and retry stalled downloads, fall back to IPv4, and fail with a diagnostic instead of hanging ([c707422](https://github.com/pungoyal/dotsync/commit/c707422ddcff3eef071829a2fe477a11081019d3))


### Performance

* **agent:** make idle syncs write nothing and re-read nothing ([#8](https://github.com/pungoyal/dotsync/issues/8)) ([f7f0815](https://github.com/pungoyal/dotsync/commit/f7f0815ec1e0ec41282a5239673eaa2682678c41))

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
