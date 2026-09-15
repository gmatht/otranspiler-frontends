#!/bin/sh
# install.sh — activate the tracked git guard in this repository.
#
# WHY THIS EXISTS
#   The guard used to live only in `.git/hooks/pre-commit`.  That directory is
#   per-checkout and untracked: it is never cloned, so the guard existed in
#   exactly the two working copies where someone had installed it by hand —
#   and did not exist at all in the new frontends repository (a fresh
#   `git init`, samples only) or in any fresh clone.  Committing the hook and
#   pointing `core.hooksPath` at it makes the guard part of the project.
#
# `core.hooksPath` is still a per-repo setting, so this must run once per clone.
# It is idempotent, and it is safe in a submodule or a worktree.
#
# Usage:  harness/git-hooks/install.sh [REPO...]      (default: this repo)
#         PIR_ALLOW_MARKERS=1  PIR_COMMIT_MAX_BYTES=... work as documented in
#         pre-commit (they are read at hook time, not install time).
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
chmod +x "$here/pre-commit" 2>/dev/null || true

targets=""
if [ "$#" -eq 0 ]; then
  # Default to the repository that CONTAINS this hooks dir, not the CWD: the
  # script is copied into each repo (sh2perl/hooks, otranspiler-frontends/hooks)
  # so `sh <that copy>` must install into THAT repo.
  targets=$(git -C "$here/.." rev-parse --show-toplevel 2>/dev/null || true)
  [ -n "$targets" ] || { echo "install.sh: $here/.. is not inside a git repository" >&2; exit 1; }
else
  targets="$*"
fi

rc=0
for repo in $targets; do
  if ! git -C "$repo" rev-parse --git-dir >/dev/null 2>&1; then
    echo "install.sh: $repo is not a git repository" >&2
    rc=1
    continue
  fi
  cur=$(git -C "$repo" config --get core.hooksPath 2>/dev/null || true)
  if [ "$cur" = "$here" ]; then
    echo "  $repo: already using $here"
    continue
  fi
  git -C "$repo" config core.hooksPath "$here"
  echo "  $repo: core.hooksPath -> $here"
  [ -n "$cur" ] && echo "    (was: $cur)"
done

cat <<EOF

The guard is now active for the repositories listed above:
  pre-commit            size (> \$PIR_COMMIT_MAX_BYTES, default 1 MiB), binary
                        blobs, and merge-conflict markers in added lines
  pre-commit --all      the same marker check over every tracked file
  pre-push              oversized blobs in the PUSH SET (shim -> the tracked
                        harness/pre-push-guard.sh, so redirecting hooksPath
                        never disables the hand-installed one it replaced)
  git commit --no-verify              bypass one commit
  PIR_ALLOW_MARKERS=1                 allow markers in an intentional fixture
  PIR_COMMIT_MAX_BYTES=<bytes>        raise the size threshold

Nothing under frontends/ is checked: the frontends are a separate repository
(otranspiler-frontends, mounted as sh2perl/frontends).  Install this guard there
too, from that repo's own copy.
EOF
exit "$rc"
