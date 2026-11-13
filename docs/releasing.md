# Releasing

Servers install and update from prebuilt images, and the CLI comes as binaries from GitHub Releases. Building from source stays available for both.

## Versions

The Git tag `vX.Y.Z` is the only place a version is written. Everything else follows it:

| What | Version |
| --- | --- |
| Git tag | `vX.Y.Z`, or `vX.Y.Z-rc.N` for a pre-release |
| `control` and CLI binaries (`brand.Version`, set by ldflags) | `X.Y.Z` |
| Images `ghcr.io/b0balyson/dropsite-control` and `ghcr.io/b0balyson/dropsite-caddy` | tags `X.Y.Z`, `X.Y` and `latest`; a pre-release gets only `X.Y.Z-rc.N` |
| GitHub Release | named after the tag, with the CLI binaries and `SHA256SUMS` |
| `CHANGELOG.md` | section `## [X.Y.Z] - YYYY-MM-DD` |
| Any other build | `dev+<commit>` (`dev+<commit>.dirty` with uncommitted changes), from `scripts/version.sh`; `dev` without Git metadata |

`deploy/.env.example` keeps `DROPSITE_VERSION=latest` and `brand.Version` keeps the default `dev`. `make release-check` (also a CI job) fails when either changes, when a doc, script or deploy file hardcodes a release version, or, for a tag, when `CHANGELOG.md` has no section for it. Use `X.Y.Z` or `<version>` in examples.

## Release procedure

From an up-to-date `main` with a clean tree:

```bash
git switch main
git pull --ff-only
make fmt-check lint test release-test lint-actions

# Generate the CHANGELOG.md section from the Conventional Commits since the
# previous tag, then read and edit it: it becomes the release notes.
make changelog V=X.Y.Z
make release-check TAG=vX.Y.Z

git add CHANGELOG.md
git commit -m "chore(release): vX.Y.Z"
git push origin main

git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
gh run watch "$(gh run list --workflow release --limit 1 --json databaseId --jq '.[0].databaseId')"
```

`make release-notes` previews the notes of the commits since the last tag without touching any file (`make release-notes FROM=vA.B.C TO=vX.Y.Z` for any range). Commit types map to sections: `feat` Added, `fix` Fixed, `perf`/`refactor`/`revert` Changed, `docs` Documentation, `type!:` or a `BREAKING CHANGE:` footer Breaking changes; `build`, `chore`, `ci`, `style` and `test` are left out.

A pre-release works the same way with `V=X.Y.Z-rc.1` and the tag `vX.Y.Z-rc.1`: it gets no `X.Y` and no `latest` image tag, is marked as a pre-release on GitHub, and servers on `latest` do not pick it up.

### What the tag push runs

[`.github/workflows/release.yml`](../.github/workflows/release.yml), every action pinned by commit:

1. **verify**: the tag format, `scripts/release-check.sh vX.Y.Z`, gofmt, golangci-lint, `go test -race`, the release script tests.
2. **images** (after verify): `docker buildx` builds `control` and `caddy` for linux/amd64 and linux/arm64 (cross-compiled, no emulation) with `VERSION=X.Y.Z`, OCI labels and annotations (`source`, `version`, `revision`, `created`, `title`, `description`, `licenses`), an SBOM and `mode=max` provenance, pushes them to ghcr.io, and adds a signed build provenance attestation (`actions/attest-build-provenance`, Sigstore) to the registry.
3. **cli** (after verify): `make cli-release VERSION=X.Y.Z` for linux, macOS and Windows on amd64 and arm64, checks the embedded version, attests every binary.
4. **release** (after images and cli): verifies `SHA256SUMS` and creates the GitHub Release with the binaries, `SHA256SUMS` and the `CHANGELOG.md` section plus install and verify instructions as notes.

Nothing is published when verify fails. When a later job fails, fix the cause and re-run the failed jobs of the same run; do not move a tag that has been pushed. A release that turns out broken is fixed by the next patch release.

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on every push to `main` and every pull request: gofmt, golangci-lint, shellcheck, actionlint, `scripts/release-check.sh`, the release script tests, a preview of the release notes, `go test -race`, the installer checks, the CLI for all platforms and both images for both architectures (built, not pushed). The end-to-end suites (`make e2e`, `make install-e2e`) run locally.

### First release only

- The first push creates the packages `dropsite-control` and `dropsite-caddy` under the account. GitHub creates container packages as **private**: open each package (github.com/b0balYson?tab=packages → package → Package settings), set **Change visibility → Public**, and check that the package is linked to the repository (the image label `org.opencontainers.image.source` does that). Until then servers cannot pull and fall back to a source build.
- Check anonymously: `docker logout ghcr.io; docker pull ghcr.io/b0balyson/dropsite-control:X.Y.Z`.
- `CHANGELOG.md` gets the whole history in its first section: shorten it by hand before tagging.

### Verify a release

```bash
docker buildx imagetools inspect ghcr.io/b0balyson/dropsite-control:X.Y.Z   # both platforms
gh attestation verify oci://ghcr.io/b0balyson/dropsite-control:X.Y.Z --repo b0balYson/dropsite
gh attestation verify oci://ghcr.io/b0balyson/dropsite-caddy:X.Y.Z --repo b0balYson/dropsite
docker run --rm ghcr.io/b0balyson/dropsite-control:X.Y.Z version   # dropsite control X.Y.Z
gh release download vX.Y.Z --repo b0balYson/dropsite --pattern 'dropsite-linux-amd64' --pattern SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify dropsite-linux-amd64 --repo b0balYson/dropsite
```

## On a server

Servers set up by the [one-command installer](install.md#one-command-installer) follow `DROPSITE_VERSION` in `/opt/dropsite/deploy/.env`:

- `latest` (default): `dropsite-update` backs up, switches the checkout to the newest release tag (pre-releases are skipped), downloads its images and restarts. Until the release workflow has pushed the images, an update builds that tag from source instead, so run updates after the workflow has finished.
- `X.Y.Z`: that release, also after later releases.
- `DROPSITE_BUILD=1 dropsite-update`: the newest commit of the branch, built on the server (needs the swap the installer adds on small servers).

The checkout always matches the running images, so the compose file and the Caddyfile belong to the same release. `dropsite-update` records the running version, commit, image and backup before it changes anything.

### Roll back

- **The last update**: `dropsite-rollback`. It checks out the previous commit, starts the images of the previous version (downloading them again if needed, or rebuilding a source version) and restores the backup taken right before the update. Data written since that update is lost.
- **A specific release**: set `DROPSITE_VERSION=<old>` in `.env` and run `dropsite-update`. This only works when no database migration happened in between: `control` refuses to start on a database of a newer version (`database schema is newer than this binary`). Otherwise restore a backup made by that release: see [backup.md](backup.md#restore).
- Back to following releases: `DROPSITE_VERSION=latest` and `dropsite-update`.

By hand (no installer helpers) the same steps are in [backup.md](backup.md#upgrade).

## Local checks before a tag

None of these push or publish anything:

```bash
make release-check TAG=vX.Y.Z      # after make changelog V=X.Y.Z
make release-test                  # release scripts against throwaway repositories
make release-notes                 # notes since the last tag
make lint-actions                  # actionlint (.tools/bin/actionlint from make tools, else go run)
docker buildx create --name dropsite-release-test
docker buildx build --builder dropsite-release-test --platform linux/amd64,linux/arm64 \
  -f deploy/Dockerfile --build-arg VERSION=X.Y.Z .
docker buildx rm dropsite-release-test
```
