#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Copies files of an npm package into web/third_party/<package> without
# Node.js. The tarball is checked against the sha512 integrity published by
# the registry before anything is extracted, and web/third_party/manifest.json
# records the source, the license and the SHA-256 of every copied file. The
# test suite verifies those hashes, so a vendored file cannot change unnoticed.
#
# Usage: scripts/vendor-npm.sh <package> <version> <path inside the package>...
set -euo pipefail

if [ $# -lt 3 ]; then
  echo "usage: $0 <package> <version> <path inside the package>..." >&2
  exit 2
fi
name=$1
version=$2
shift 2
# A scoped package such as @ibm/plex-sans keeps its scope as a directory.
if ! [[ $name =~ ^(@[a-z0-9._-]+/)?[a-z0-9._-]+$ ]]; then
  echo "invalid package name: $name" >&2
  exit 2
fi
case $version in *[!0-9A-Za-z.+-]* | "") echo "invalid version: $version" >&2; exit 2 ;; esac

root=$(git rev-parse --show-toplevel)
dest="$root/web/third_party/$name"
manifest="$root/web/third_party/manifest.json"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

meta=$(curl -fsS "https://registry.npmjs.org/$name/$version")
tarball=$(jq -r .dist.tarball <<<"$meta")
integrity=$(jq -r .dist.integrity <<<"$meta")
license=$(jq -r .license <<<"$meta")
case $tarball in "https://registry.npmjs.org/$name/-/"*) ;; *) echo "unexpected tarball URL: $tarball" >&2; exit 1 ;; esac
case $integrity in sha512-*) ;; *) echo "the registry publishes no sha512 integrity for $name@$version" >&2; exit 1 ;; esac

curl -fsSL -o "$tmp/package.tgz" "$tarball"
got="sha512-$(openssl dgst -sha512 -binary "$tmp/package.tgz" | base64 -w0)"
if [ "$got" != "$integrity" ]; then
  echo "integrity mismatch for $name@$version" >&2
  exit 1
fi

mkdir "$tmp/x"
tar -xzf "$tmp/package.tgz" -C "$tmp/x" --no-same-owner --no-same-permissions
src="$tmp/x/package"

license_file=""
for f in LICENSE LICENSE.md LICENSE.txt license OFL.txt; do
  if [ -f "$src/$f" ]; then
    license_file=$f
    break
  fi
done
if [ -z "$license_file" ]; then
  echo "$name@$version ships no license file" >&2
  exit 1
fi

rm -rf "$dest"
for f in package.json "$license_file" "$@"; do
  case $f in /* | *..*) echo "invalid path: $f" >&2; exit 2 ;; esac
  if [ ! -f "$src/$f" ]; then
    echo "$f does not exist in $name@$version" >&2
    exit 1
  fi
  install -D -m 0644 "$src/$f" "$dest/$f"
done

entry=$(
  cd "$dest" &&
    find . -type f | sed 's|^\./||' | LC_ALL=C sort | while read -r f; do
      printf '%s\t%s\n' "$f" "$(sha256sum "$f" | cut -d' ' -f1)"
    done | jq -R -s \
      --arg name "$name" --arg version "$version" --arg license "$license" \
      --arg license_file "$license_file" --arg source "$tarball" --arg integrity "$integrity" \
      '{name: $name, version: $version, license: $license, license_file: $license_file,
        source: $source, integrity: $integrity,
        files: (split("\n") | map(select(length > 0) | split("\t") | {(.[0]): .[1]}) | add)}'
)
if [ ! -f "$manifest" ]; then
  echo '{"packages": []}' >"$manifest"
fi
jq --argjson e "$entry" '.packages = ([.packages[] | select(.name != $e.name)] + [$e] | sort_by(.name))' \
  "$manifest" >"$tmp/manifest.json"
mv "$tmp/manifest.json" "$manifest"
echo "vendored $name@$version ($license) into web/third_party/$name"
