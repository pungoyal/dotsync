# Verifying releases

Every release is built from a tagged commit by GitHub Actions. Nobody builds release binaries on a laptop. Each release publishes:

| artifact | purpose |
|---|---|
| `dotsync_<version>_<os>_<arch>.tar.gz` | the binary, plus `LICENSE`, `README.md` and `docs/` |
| `checksums.txt` | SHA-256 of every archive |
| `*.sbom.json` | a software bill of materials (SPDX) for every archive |
| build provenance attestation | a signed [SLSA provenance](https://slsa.dev/provenance) statement linking each archive to the exact workflow run, commit and repository that built it |

The provenance attestations are created with GitHub's [artifact attestations](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations), signed through Sigstore with the workflow's own OIDC identity. No long-lived signing key exists that could leak.

## Verify the provenance (recommended)

With the [GitHub CLI](https://cli.github.com) 2.49 or newer:

```sh
gh attestation verify dotsync_1.2.3_darwin_arm64.tar.gz --repo pungoyal/dotsync
```

A successful check shows that this exact file was built by this repository's release workflow from a commit in this repository. The install script runs this check automatically when `gh` is available; set `DOTSYNC_REQUIRE_ATTESTATION=1` to make a missing or failed check fatal.

To see exactly which workflow, commit and runner produced the file:

```sh
gh attestation verify dotsync_1.2.3_darwin_arm64.tar.gz --repo pungoyal/dotsync --format json \
  | jq '.[0].verificationResult.signature.certificate | {sourceRepositoryRef, sourceRepositoryDigest, buildConfigURI, runInvocationURI}'
```

## Verify the checksum

```sh
curl -fsSLO https://github.com/pungoyal/dotsync/releases/download/v1.2.3/checksums.txt
shasum -a 256 --check --ignore-missing checksums.txt     # macOS
sha256sum --check --ignore-missing checksums.txt         # Linux
```

A checksum only proves the download wasn't corrupted, and that it matches a `checksums.txt` which could itself be replaced. The attestation proves where the file came from, which is why it's the recommended check.

## Reproducing a build

Release binaries are built with `CGO_ENABLED=0`, `-trimpath` and a fixed modification time (the commit date). With the same Go version, `goreleaser build --clean --single-target` on the tagged commit should produce byte-identical binaries.
