#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
script="$repo_root/.github/scripts/next-release-tag.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cd "$tmp"
git init --quiet
git config user.name 'Release Test'
git config user.email 'release-test@example.invalid'

git commit --allow-empty --quiet -m 'feat: first release'
expect() {
	local want=$1
	local got
	got=$($script)
	if [ "$got" != "$want" ]; then
		printf 'next-release-tag = %q, want %q\n' "$got" "$want" >&2
		exit 1
	fi
}

expect 'v0.1.0'
git tag -a v0.1.0 -m 'release v0.1.0'

# A rerun after tag creation must release the existing tag, not skip it.
expect 'v0.1.0'

git commit --allow-empty --quiet -m 'docs: explain releases'
expect ''

git commit --allow-empty --quiet -m 'fix: repair installer'
expect 'v0.1.1'
git tag -a v0.1.1 -m 'release v0.1.1'

git commit --allow-empty --quiet -m 'refactor!: replace extension handshake'
expect 'v0.2.0'
