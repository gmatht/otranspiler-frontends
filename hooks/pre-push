#!/bin/sh
# pre-push guard — refuse a push that would send an oversized blob.
#
# The .git/hooks/pre-commit "pir guard" only sees the index of one worktree,
# so it cannot catch artifacts that entered via `--no-verify`, a merge, a
# fetch, or `git filter-repo`/`fast-import`. This guard walks the objects
# actually being PUSHED and blocks GitHub's hard limit before the round trip.
#
# Install:  harness/git-hooks/install.sh [REPO...]   (idempotent, per clone)
#   That sets core.hooksPath to the TRACKED harness/git-hooks/, whose
#   pre-push is a shim exec-ing this file — so hand-copying is gone, and the
#   guard now exists in any clone that runs the installer.  NOTE for anyone
#   keeping a hand-installed .git/hooks/pre-push: core.hooksPath REDIRECTS
#   EVERY hook, so that copy becomes dead; use the installer instead.
#
# Config: PIR_PUSH_MAX_BYTES (default 100 MiB — GitHub's hard limit)
#         PIR_PUSH_WARN_BYTES (default 50 MiB — GitHub warns above this)
# Bypass for one push: git push --no-verify
set -u

MAX="${PIR_PUSH_MAX_BYTES:-104857600}"
WARN="${PIR_PUSH_WARN_BYTES:-52428800}"
bad=0
tmp="$(mktemp)"
trap 'rm -f "$tmp" "$tmp.sizes"' EXIT

while read -r _local_ref local_sha _remote_ref remote_sha; do
  case "${local_sha:-}" in
    ""|0000000000000000000000000000000000000000) continue ;;
  esac
  case "${remote_sha:-}" in
    ""|0000000000000000000000000000000000000000) range="$local_sha" ;;
    *) range="$remote_sha..$local_sha" ;;
  esac
  git rev-list --objects "$range" 2>/dev/null > "$tmp" || continue
  awk '{print $1}' "$tmp" \
    | git cat-file --batch-check='%(objecttype) %(objectname) %(objectsize)' 2>/dev/null \
    | awk -v warn="$WARN" -v max="$MAX" '
        $1=="blob" {
          if (($3+0) > max) print "ERR", $2, $3;
          else if (($3+0) > warn) print "WARN", $2, $3;
        }' > "$tmp.sizes"

  while read -r kind oid size; do
    path="$(awk -v o="$oid" '$1==o {$1=""; sub(/^ /,""); print; exit}' "$tmp")"
    mib=$((size / 1048576))
    if [ "$kind" = ERR ]; then
      echo "pre-push guard: $oid is ${mib} MiB (> $((MAX/1048576)) MiB limit): ${path:-?}" >&2
      bad=1
    else
      echo "pre-push guard: warning — ${mib} MiB blob (GitHub warns >$((WARN/1048576)) MiB): ${path:-?}" >&2
    fi
  done < "$tmp.sizes"
done

if [ "$bad" -ne 0 ]; then
  echo "pre-push guard: refusing push. Strip the blobs (docs/CLEAN_GIT.md), raise" >&2
  echo "PIR_PUSH_MAX_BYTES deliberately, or 'git push --no-verify' once." >&2
  exit 1
fi
exit 0
