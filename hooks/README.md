# hooks/ — the git guard for this repository

Tracked so the guard exists in every clone: `.git/hooks/` is per-checkout and
untracked, which is why the original "pir guard" protected only the two working
copies where someone had installed it by hand — and did not exist at all in a
fresh `git init`.

    sh hooks/install.sh          # sets core.hooksPath -> this directory (per clone)
    hooks/pre-commit --all       # marker check over every tracked file

`pre-commit` is a byte-identical copy of `sh2loop/harness/git-hooks/pre-commit`
(keep them in sync); `pre-push` is a copy of `sh2loop/harness/pre-push-guard.sh`.
Paths under `frontends/` are excluded — that tree is its own repository
(`otranspiler-frontends`), so this repo neither owns nor polices its contents.
