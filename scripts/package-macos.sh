#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
identity="${APPLE_SIGNING_IDENTITY:-}"
if [[ -z "$identity" || "$identity" == '-' ]]; then
  echo 'APPLE_SIGNING_IDENTITY must name a certificate identity; ad-hoc release signing is disabled.' >&2
  exit 1
fi
keychain_args=()
if [[ -n "${MACOS_SIGNING_KEYCHAIN_PATH:-}" ]]; then
  keychain_args=("$MACOS_SIGNING_KEYCHAIN_PATH")
fi
security find-identity -v -p codesigning "${keychain_args[@]}" | grep -F -- "\"$identity\"" >/dev/null
version="$("${PYTHON:-python3}" scripts/release.py check --env-file "${ENV_FILE:-.env}")"
release_dir="${RELEASE_DIR:-$PWD/dist}"
mkdir -p "$release_dir"
release_dir="$(cd "$release_dir" && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/maccy-package.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT
xcodebuild -project Maccy.xcodeproj -scheme Maccy -configuration Release \
  -destination 'generic/platform=macOS' -derivedDataPath "$work_dir/build" \
  ARCHS='arm64 x86_64' ONLY_ACTIVE_ARCH=NO \
  CODE_SIGN_STYLE=Manual CODE_SIGN_IDENTITY="$identity" DEVELOPMENT_TEAM= \
  ENABLE_HARDENED_RUNTIME=NO \
  build
app="$work_dir/build/Build/Products/Release/Maccy.app"
actual_version="$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$app/Contents/Info.plist")"
test "v$actual_version" = "$version"
lipo "$app/Contents/MacOS/Maccy" -verify_arch arm64 x86_64
codesign --verify --deep --strict "$app"
# A certificate-based signature must survive packaging (no ad-hoc fallback).
codesign -d --extract-certificates="$work_dir/app-cert-" "$app"
test -s "$work_dir/app-cert-0"
mkdir "$work_dir/dmg"
ditto "$app" "$work_dir/dmg/Maccy.app"
ln -s /Applications "$work_dir/dmg/Applications"
asset="Maccy-${version}-macos-universal.dmg"
hdiutil create -volname Maccy -srcfolder "$work_dir/dmg" -ov -format UDZO "$release_dir/$asset"
codesign --force --sign "$identity" --timestamp=none "$release_dir/$asset"
codesign --verify --strict "$release_dir/$asset"
codesign -d --extract-certificates="$work_dir/dmg-cert-" "$release_dir/$asset"
cmp "$work_dir/app-cert-0" "$work_dir/dmg-cert-0"
hdiutil verify "$release_dir/$asset"
(cd "$release_dir" && shasum -a 256 "$asset" > "$asset.sha256")
echo "Created $release_dir/$asset (certificate signed; not notarized)"
