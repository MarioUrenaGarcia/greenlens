#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Mario Ureña García
#
# Installs a development tool published on npm as a native binary, without
# Node.js. The tarball must match the sha512 integrity pinned in the Makefile,
# so a package replaced on the registry is refused. Nothing from the package
# is executed during the installation.
#
# Usage: scripts/install-npm-tool.sh <package> <version> <sha512 integrity> <directory>
set -euo pipefail

if [ $# -ne 4 ]; then
  echo "usage: $0 <package> <version> <sha512 integrity> <directory>" >&2
  exit 2
fi
name=$1
version=$2
integrity=$3
dir=$4
if ! [[ $name =~ ^(@[a-z0-9._-]+/)?[a-z0-9._-]+$ ]]; then
  echo "invalid package name: $name" >&2
  exit 2
fi
case $version in *[!0-9A-Za-z.+-]* | "") echo "invalid version: $version" >&2; exit 2 ;; esac
case $integrity in sha512-*) ;; *) echo "the integrity must be a sha512 value" >&2; exit 2 ;; esac

if [ -f "$dir/.integrity" ] && [ "$(cat "$dir/.integrity")" = "$integrity" ]; then
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
base=${name##*/}
curl -fsSL -o "$tmp/package.tgz" "https://registry.npmjs.org/$name/-/$base-$version.tgz"
got="sha512-$(openssl dgst -sha512 -binary "$tmp/package.tgz" | base64 -w0)"
if [ "$got" != "$integrity" ]; then
  echo "integrity mismatch for $name@$version" >&2
  exit 1
fi
mkdir "$tmp/x"
tar -xzf "$tmp/package.tgz" -C "$tmp/x" --no-same-owner
rm -rf "$dir"
mkdir -p "$(dirname "$dir")"
mv "$tmp/x/package" "$dir"
printf '%s' "$integrity" >"$dir/.integrity"
echo "installed $name@$version in $dir"
