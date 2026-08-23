# corpus-c — the C-idiom example corpus

GOOD_EXAMPLES.md conventions applied to C source (see ../../s2p.c/GOOD_EXAMPLES.md):
one or two C constructs per example, a header comment saying what is
demonstrated, diagnostic stdout that diverges the moment a lowering is
wrong, self-contained programs (no external files), descriptive numbered
names.

Gate: `harness/corpus-c.sh [--gate]` — the oracle is the native gcc
compile+run of each example; every backend target must reproduce the
same stdout. The frontend is c-sh-go (the C frontend); the same A1
feeds every backend renderer.

Coverage contract: the corpus includes at least every construct the
cpp-sh-go frontend's corpus (testdata_cpp) exercises — printf/puts,
int/char arith with bool sugar, if/else, while/do-while/for,
switch/case/default/fallthrough, functions + calls, structs, pointers
(&/*/->) incl. malloc/free, sizeof + sized ints, goto/labels, varargs
(plain and GNU typed), preprocessor conditionals, comments.
