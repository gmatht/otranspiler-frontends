# frontends/ — new frontends for the sh2perl ecosystem

Goal: **frontends parse; they do not optimize.** A frontend in any language
emits the language-neutral ShIR JSON (the A1 contract, `debashc --shir`); the
core optimizes and attaches facts (var_types/purity); backends render.

The oracle for a frontend is **byte equality with the core frontend's
`--shir` output** on its supported subset — no core modification needed.

## Layout

- `plan.md` — the plan + session learnings (read first)
- `shir-contract/schema.json` — hand-authored ShIR JSON node/field schema
- `shir-contract/check_schema.py` — corpus coverage checker (schema drift → fail)
- `py-sh/pysh.py` — first frontend: shell-subset parser → byte-identical ShIR JSON
- `equiv.py` — equivalence harness (the oracle): py-sh vs debashc --shir
- `tests/` — curated v1 examples (10 files)

## Usage

```sh
# byte-equivalence vs the core frontend (the frontend's gate)
python3 equiv.py tests/
python3 equiv.py ../sh2perl/examples          # corpus (13/13 supported pass)
python3 equiv.py --strip-annotations tests/   # semantic IR only (no var_types/purity)

# contract schema check over core output (any drift fails loudly)
python3 shir-contract/check_schema.py ../sh2perl/examples/*.sh

# the frontend itself
python3 py-sh/pysh.py --shir tests/t01_echo.sh --raw
```

## Status (session 1)

- 13/13 corpus examples byte-identical; 0 FAIL; 514 fail-loud unsupported
  (outside the v1 subset); schema checker 0/70 violations.
- v1 subset: comments, `;` separators, `name=value` assignments, simple
  commands with bare/single/double-quoted words + `$var` refs, `VAR=x cmd`
  env prefixes. Everything else raises `Unsupported` — refuse, never guess.

## Discipline

1. **Never guess a shape**: every new construct is added by pinning the
   core's exact `--shir` output first (probe → implement → oracle-confirm).
2. **Refuse > guess**: unproven constructs fail loud, they don't half-parse.
3. **Read the core, never write it** (the ESTree worker stages `sh2perl/src/*`
   and `harness/*`); new files live only under `frontends/`.
4. **Measure with exact bytes** (Python heredoc), never through outer-shell
   quoting.
