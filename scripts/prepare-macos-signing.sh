#!/usr/bin/env bash
# CI only: import the persistent release identity into an ephemeral runner keychain.
set -euo pipefail
[[ "${GITHUB_ACTIONS:-}" == true ]] || { echo 'Run this script only on an ephemeral GitHub Actions runner.' >&2; exit 1; }
: "${RUNNER_TEMP:?}"
: "${GITHUB_ENV:?}"
: "${APPLE_CERTIFICATE:?Missing APPLE_CERTIFICATE (base64 PKCS#12)}"
: "${APPLE_CERTIFICATE_PASSWORD:?Missing APPLE_CERTIFICATE_PASSWORD}"
: "${APPLE_SIGNING_IDENTITY:?Missing APPLE_SIGNING_IDENTITY}"
[[ "$APPLE_SIGNING_IDENTITY" != '-' ]] || { echo 'A certificate signing identity is required.' >&2; exit 1; }

umask 077
certificate="$RUNNER_TEMP/maccy-signing.p12"
pem="$RUNNER_TEMP/maccy-signing.pem"
keychain="$RUNNER_TEMP/maccy-signing.keychain-db"
trap 'rm -f "$certificate" "$pem"' EXIT
keychain_password="$(openssl rand -hex 24)"
echo "::add-mask::$keychain_password"
printf '%s' "$APPLE_CERTIFICATE" | /usr/bin/base64 -D > "$certificate"
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security import "$certificate" -k "$keychain" -P "$APPLE_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
security set-key-partition-list -S apple-tool:,apple: -s -k "$keychain_password" "$keychain" >/dev/null

openssl pkcs12 -in "$certificate" -clcerts -nokeys -passin env:APPLE_CERTIFICATE_PASSWORD |
  openssl x509 -out "$pem"
subject="$(openssl x509 -in "$pem" -noout -subject -nameopt RFC2253)"
issuer="$(openssl x509 -in "$pem" -noout -issuer -nameopt RFC2253)"
if [[ "${subject#subject=}" == "${issuer#issuer=}" ]]; then
  # As in Axonkey: administrator trust avoids interactive user-keychain prompts.
  sudo -n security add-trusted-cert -d -r trustRoot -p codeSign \
    -k /Library/Keychains/System.keychain "$pem"
fi

current_keychains=()
while IFS= read -r current_keychain; do
  current_keychains+=("${current_keychain//\"/}")
done < <(security list-keychains -d user)
security list-keychains -d user -s "$keychain" "${current_keychains[@]}"
security find-identity -v -p codesigning "$keychain" | grep -F -- "\"$APPLE_SIGNING_IDENTITY\"" >/dev/null
echo "MACOS_SIGNING_KEYCHAIN_PATH=$keychain" >> "$GITHUB_ENV"
