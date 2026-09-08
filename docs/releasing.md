# Build and release

This fork uses the same local release contract as Kivo/Axonkey: `.env` provides
the version, `make release` updates metadata and creates a commit plus an annotated
tag, and pushing the tag triggers GitHub Actions. It never pushes automatically.

## Local commands

Requires Xcode 26+ with macOS SDK, Python 3.9+ and Git. The server requires the Go
version declared in `server/go.mod`.

```sh
cp .env.example .env  # once after cloning; do not overwrite existing local settings
make build           # Debug app, /tmp/maccy-local-build/Maccy.app
make build-release   # optimized app, dist/Maccy.app (the former make release)
APPLE_SIGNING_IDENTITY="Axonkey Self-Signed Code Signing" make package
                     # signed universal arm64 + x86_64 DMG and SHA-256 in dist/
make version-check
make test-release
make server-test
```

Builds do not change the version, commit or tag. `make package` validates version
agreement, verifies both executable architectures and the app's code signature,
signs the DMG with the same certificate, and checks both leaf certificates match.
Checksums are generated after signing. `make package` requires a valid certificate
identity in the keychain and rejects missing identities and `-` (ad-hoc signing).
Local Debug and `build-release` builds still use ad-hoc signing. Swift dependencies are recorded in
`Maccy.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved`.

## Create a release

Commit all intended changes first; staged, unstaged and non-ignored untracked
files cause release to fail before changing anything.

```sh
make release           # v2.7.1 -> v2.7.2
# Or choose a greater version explicitly:
make release V=v2.8.0
```

The two commands above are alternatives. Version values must be exactly
`vMAJOR.MINOR.PATCH`. The script checks `.env`, `.env.example`, and both Xcode
configurations agree, rejects existing tags, updates `MARKETING_VERSION`, increments
the integer `CURRENT_PROJECT_VERSION`, and commits only version files with
`chore: release vX.Y.Z`. `.env` is ignored; its other settings are preserved.

After inspecting the release commit and tag, publish the branch and that specific
tag yourself, for example:

```sh
git push origin HEAD
git push origin v2.7.2  # replace with the tag just created
```

If a Git hook, commit, or tag command fails, the script stops and leaves the state
available for inspection. It does not reset files or delete commits. Resolve the
reported failure and inspect `git status`/`git log` before completing the release;
do not blindly rerun the bump command.

## GitHub Actions

Both macOS jobs select Xcode 26.3 explicitly: the app uses `NSGlassEffectView`,
which requires the macOS 26 SDK at compile time. Xcode 26.3 is listed in the
[macos-15 runner inventory](https://github.com/actions/runner-images/blob/main/images/macos/macos-15-Readme.md#xcode).

- Pull requests and branch pushes run version/release-tool tests, Go tests with
  an ephemeral PostgreSQL service, `go vet`, and a macOS Release build.
- `v*` tag pushes run the same checks, reject non-semver or mismatched tags,
  then build `Maccy-vX.Y.Z-macos-universal.dmg` and its `.sha256`.
- After packaging, the server job builds and pushes the `linux/amd64` image to
  `registry.cn-heyuan.aliyuncs.com/leo03w/maccy-server:vX.Y.Z` using the existing
  `server/Dockerfile`. It does not overwrite `latest`.
- Only after successful checks, packaging and image publication, a separate job with
  `contents: write` uploads the assets and publishes the GitHub Release. New
  releases remain drafts until asset upload succeeds; reruns replace matching assets.

Enable GitHub Actions in the fork and add repository secrets
`ALIYUN_REGISTRY_USERNAME` and `ALIYUN_REGISTRY_PASSWORD` with push access to that
image repository (the same names used by Orbit). Missing credentials fail the
image job. GitHub Release publication uses the workflow's `GITHUB_TOKEN`.

### Self-signed macOS releases

Configure the same application-signing secrets as Axonkey:

| Secret | Value |
| --- | --- |
| `APPLE_CERTIFICATE` | Base64-encoded PKCS#12 (`.p12`) containing the application certificate and private key |
| `APPLE_CERTIFICATE_PASSWORD` | Password protecting that PKCS#12 file |
| `APPLE_SIGNING_IDENTITY` | Exact identity name, for example `Axonkey Self-Signed Code Signing` |

Reuse the existing certificate and private key across releases. The name alone
does not preserve the signing identity if a new certificate is generated.
Maccy ships an App inside a DMG, not a PKG, so Axonkey's separate
`MACOS_INSTALLER_*` certificate is not needed.

The tag workflow requires all three secrets. It imports the certificate into a
temporary keychain, enables noninteractive `codesign` access, and trusts the
self-signed root for code signing using `sudo -n security add-trusted-cert -d`
on the ephemeral GitHub runner, as Axonkey does. The keychain is deleted even if
the build fails. Certificate import is CI-only; local packaging uses an already
installed identity and does not change system trust settings.

Both App and DMG are certificate-signed. No fallback to ad-hoc signatures is
allowed for a release. Self-signing is **not Apple notarization**: Gatekeeper
may still block downloads on Macs that do not trust this certificate. This
pipeline does not submit artifacts to Apple's notarization service.

Swift UI/clipboard tests are not run on hosted runners because they depend on
interactive desktop state and Accessibility permissions. Go tests requiring
`POTION_TEST_MODEL_DIR`, `ZVEC_LIBRARY_PATH`, or Jieba dictionaries skip unless those
fixtures are supplied. PostgreSQL integration tests do run in CI.

Install fork updates manually from this repository's GitHub Releases. The existing
Sparkle feed still points to upstream Maccy; this pipeline does not update that
feed or provide automatic fork updates. Image publication does not deploy a
running service. On the deployment host, set `version=vX.Y.Z` in `server/.env`
and run `docker compose -f server/compose.yaml --env-file server/.env pull api`
then `docker compose -f server/compose.yaml --env-file server/.env up -d api`.
The host's runtime configuration, database network and persistent volume follow
`server/README.md`. `server/.env` is deployment state, not a source version file.
