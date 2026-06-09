#!/usr/bin/env sh
# Sign a release artifact with minisign when a signing key is configured.
#
# Invoked by goreleaser's signs block as: sign-release.sh <artifact> <signature>
#
# MINISIGN_KEY must contain an UNENCRYPTED minisign secret key (generate with
# `minisign -G -W`). Its secrecy comes from the CI secret store, so no
# interactive passphrase prompt can hang the release. When MINISIGN_KEY is
# unset, signing is skipped and the release still publishes SHA-256 checksums.
#
# Verify a download with:  minisign -Vm checksums.txt -P <public key>
set -eu

if [ "$#" -lt 2 ]; then
	echo "Usage: $0 <artifact> <signature>" >&2
	exit 1
fi

artifact="$1"
signature="$2"

if [ -z "${MINISIGN_KEY:-}" ]; then
	echo "MINISIGN_KEY not set; skipping signature for ${artifact}"
	exit 0
fi

# Restrict the key tempfile to the owner. mktemp already creates 0600 files,
# but umask 077 is cheap defense-in-depth for the unencrypted signing key.
umask 077
key="$(mktemp)"
trap 'rm -f "$key"' EXIT
printf '%s' "$MINISIGN_KEY" >"$key"
minisign -S -s "$key" -m "$artifact" -x "$signature"
