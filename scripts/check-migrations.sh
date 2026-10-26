#!/usr/bin/env bash
# Guards migrations/ against the failures that only show up once two PRs land
# or a release is upgraded:
#
#   - golang-migrate refuses a directory where two files share a version, and
#     the binary then fails at boot. Gaps between versions are fine.
#   - golang-migrate applies only versions above the database's current one. A
#     migration numbered at or below a version that main (the cloud) or a
#     release (self-hosters) already has is never applied to those databases.
#     So every migration a ref lacks must be numbered above that ref's highest.
#
# Usage: check-migrations.sh [--against <ref>]... [dir]
#
# Each --against ref must exist. Without one it checks against origin/main and
# the newest v* tag that are present, so `make check-migrations` and `pnpm qa`
# work offline. CI passes the commit the change lands on and the last release.
set -euo pipefail

refs=()
while [[ $# -gt 0 ]]; do
  case $1 in
    --against) refs+=("${2:?--against needs a ref}"); shift 2 ;;
    -*) echo "check-migrations: unknown option $1" >&2; exit 2 ;;
    *) break ;;
  esac
done

DIR="${1:-migrations}"
[[ -d $DIR ]] || { echo "check-migrations: no such directory: $DIR" >&2; exit 1; }

status=0
fail() { echo "check-migrations: $*" >&2; status=1; }

shopt -s nullglob
sql=("$DIR"/*.sql)
[[ ${#sql[@]} -gt 0 ]] || { echo "check-migrations: no migrations in $DIR" >&2; exit 1; }

for path in "${sql[@]}"; do
  name=$(basename "$path")
  [[ $name =~ ^[0-9]{6}_[a-z0-9_]+\.(up|down)\.sql$ ]] ||
    fail "bad filename '$name' (expected 000123_snake_case_name.up.sql / .down.sql)"
done

ups=("$DIR"/*.up.sql)
dupes=$(printf '%s\n' "${ups[@]}" | xargs -n1 basename | cut -d_ -f1 | sort | uniq -d)
for version in $dupes; do
  fail "duplicate migration version $version:"
  for path in "$DIR/${version}_"*.up.sql; do echo "    $path" >&2; done
done
[[ -z $dupes ]] || echo "check-migrations: renumber the migration that has NOT been released above the highest existing one." >&2

for path in "${ups[@]}"; do
  [[ -f ${path%.up.sql}.down.sql ]] || fail "missing $(basename "${path%.up.sql}").down.sql"
done
for path in "$DIR"/*.down.sql; do
  [[ -f ${path%.down.sql}.up.sql ]] || fail "missing $(basename "${path%.down.sql}").up.sql"
done

# Order against refs. The path inside the repository, for git ls-tree.
if ! prefix=$(git -C "$DIR" rev-parse --show-prefix 2>/dev/null); then
  [[ ${#refs[@]} -eq 0 ]] || { echo "check-migrations: $DIR is not in a git repository" >&2; exit 1; }
  prefix=""
fi
if [[ ${#refs[@]} -eq 0 && -n $prefix ]]; then
  git -C "$DIR" rev-parse -q --verify 'origin/main^{commit}' >/dev/null && refs+=(origin/main)
  tag=$(git -C "$DIR" tag --list 'v*' --sort=-v:refname | head -n1)
  [[ -n $tag ]] && refs+=("$tag")
  [[ ${#refs[@]} -gt 0 ]] || echo "check-migrations: no origin/main or v* tag here, so the order against them is not checked." >&2
fi

checked=""
for ref in "${refs[@]}"; do
  git -C "$DIR" rev-parse -q --verify "$ref^{commit}" >/dev/null ||
    { echo "check-migrations: unknown ref $ref (fetch it, with full history for tags)" >&2; exit 1; }
  # Versions at ref, ascending. A ref from before the directory existed has none.
  theirs=$(git -C "$DIR" ls-tree --full-tree --name-only "$ref:${prefix%/}" 2>/dev/null |
    sed -n 's/^\([0-9]\{6\}\)_.*\.up\.sql$/\1/p' | sort -u || true)
  [[ -n $theirs ]] || continue
  highest=$(tail -n1 <<<"$theirs")
  for path in "${ups[@]}"; do
    name=$(basename "$path")
    version=${name%%_*}
    grep -qx "$version" <<<"$theirs" && continue
    ((10#$version > 10#$highest)) ||
      fail "$name is not on $ref and is numbered at or below $highest, the highest there: databases already at $highest never apply it. Renumber it above the highest existing migration."
  done
  checked+=", new ones above $highest ($ref)"
done

[[ $status -eq 0 ]] || exit 1
echo "check-migrations: ${#ups[@]} migrations, no duplicate versions, every up has its down${checked}."
