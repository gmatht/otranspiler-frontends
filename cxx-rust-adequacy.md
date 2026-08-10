# Is shIR adequate for C++ and Rust? — an investigation
#
# **This verdict is the boundary.** `CPP_PLAN.md` is the *pursuit* plan built
# on it: parse full C and full C++14+ with tree-sitter, lower the expressible
# subset (~3/40 features here), refuse the rest cleanly on the AST.

**Verdict: no, not as currently designed.** The IR is a *shell-domain* representation:
the nodes model shell programs (commands, redirections, expansions, env
prefixes, word splitting, globbing, process pipelines, fd tables, signal
traps, $?, $$). A C++ or Rust source lowering would have to express
language features that have no correspondence in any of these — classes,
templates, RAII, lifetimes, ownership, traits, references, exceptions,
pattern matching, async, modules — by either (a) smearing them into the
existing nodes (lossy and semantically wrong) or (b) refusing them
entirely. The verbatim bridge (RawText) covers *unknown* shell fragments,
but it can't carry a typed C++/Rust program through a shIR round-trip:
the consumer would have to parse raw text, which is exactly what the
pipeline exists to avoid.

The plan §3 and §8 already named this ("wrong direction of abstraction"
for general-purpose languages, shell-domain IR). This document is the
concrete per-feature map that backs that claim, and the design choices
for C++/Rust if we want to pursue them.

## 1. What shIR actually is

The IR node vocabulary is defined in `sh2perl/src/ir.rs` and serialized
by `sh2perl/src/shir_json.rs`. The full inventory:

**Statements (25):** `Output, WriteFile, Assign, Declare, DeclareArray, If,
For, While, DoWhile, Die, Warn, Exec, Pipeline, Return, Exit, SetChildError,
Require, RawText, Case, Redirect, Function, Subshell, Background, Block, Expr`.

**Expressions (21):** `Int, Str, Var, Index, BinOp, Call, MethodCall,
Ternary, DefinedOr, Interpolate, Capture, Regex, Range, RawExpr, Arrow,
Array, Arith, Bool, Json, Ident, Object`.

**Arith (8):** `Num, Var, Index, Bin, Un, Cond, Assign, IncDec`.

**Enums:** `StrStyle {SingleQuoted, DoubleQuoted, Command, Heredoc}`,
`Sigil {Scalar, Array, Hash}` (optional), `BinOpKind` (20 arithmetic
+ comparison operators), `IrType {Int, Str, Any}` (var_types annotation).

The neutral subset (ESTree-path-only, not handled by the Perl renderer
as of 341bb40) is: `Arrow, Array, Arith, Bool, Json, Ident, Object`
expressions and `Function, Subshell, Background, Block, Expr` statements.
These exist *because the sh2 backend needs them* — they model
sh2.* runtime closures, env maps, literal JSON config blobs, subshell
copy semantics, async/background tasks, scope blocks, and bare-expression
statements. They're shell-flavored even when they're "structural."

**The IR is a shell process model with string-typed variables and a small
typed arith extension.** That's the design point. The plan §8 calls it
"shell-domain"; the docs call it "language-neutral IR for shell backends."
The "neutral" in the doc means "no sigils / no Perl-flavored markers"
— it is *not* general-purpose.

## 2. C++ feature map

| Feature | shIR expressible? | Why / why not |
|---|---|---|
| Assignment `x = 1` | yes | `Assign { targets, expr }` with `Str`/`Int` |
| `int x;` (uninitialized) | partial | `Declare { vars, init: None }` — but no type info, no uninitialized sentinel; the IR treats all vars as strings or arith-ints |
| `const int x = 1;` | no | no `const`/`readonly` flag on `Declare`/`AssignTarget` |
| `int x{1};` (brace init) | no | no structured init — `Str` is the only "literal" string kind |
| `int* p;` / references | no | no address-of / deref; `Index` is a shell-style array index, not C++ `*p` |
| `&x` (reference / address-of) | no | no reference concept |
| `class Foo { ... };` | no | no type/struct/class node; `Assign` is a name→expr binding, not a field/aggregate |
| `struct` | no | same — no aggregate/record node |
| `Foo::method()` (member) | partial | `MethodCall` exists but with a single receiver, no static dispatch, no overload set |
| `namespace ns { ... }` | no | no scope/namespace; `Block`/`Function` are name-bound but not nested namespaces |
| `template<T> ...` | no | no generics; no monomorphization concept; no type parameters |
| exceptions `try/catch/throw` | no | `Die`/`Warn` exist for fatal errors but no catch; no exception object |
| operator overloading | no | `BinOp` is hardcoded enum; no user-defined operators |
| `const char* s;` / C strings | partial | `Str` is a string; no char vs string distinction, no pointer |
| `void` / `nullptr` | no | no void/unit type; no null pointer |
| `auto x = ...;` | no | no type inference at the IR level; the IR is dynamically typed |
| `std::vector<T> v;` / STL containers | no | `Array` is a heterogeneous tuple; no typed vector/map/set; no generic container |
| references (`Foo& r = x;`) | no | no reference semantics |
| `constexpr` / compile-time eval | no | all evaluation is runtime |
| lambdas | no | `Arrow` is a shell-closure (for sh2.* dispatch), not a typed lambda; no capture, no typed params |
| `constexpr` strings, `std::string_view` | no | no string-view / lifetime concept |
| `noexcept` / move semantics | no | no exceptions model; no ownership/RAII |
| destructor / RAII | no | no lifetime model; resources aren't represented |
| `#include` | no | no module/import model (no real `imports` field semantics; the IR's `imports: Vec<String>` is a placeholder) |
| `consteval` / `if constexpr` | no | no compile-time computation |
| `std::move` / `std::forward` | no | no value categories |
| `auto [a,b] = pair;` (structured bindings) | no | no destructuring |
| `decltype` | no | no type system |
| `virtual` / dynamic dispatch | no | no vtable; no runtime type info |
| `nullptr_t` | no | no null |
| `std::span<T>` | no | no view/non-owning types |
| `std::optional<T>`, `std::variant<T>`, `std::any` | no | no sum types |
| concepts / SFINAE | no | no constraints |
| modules (`import std;`) | no | no module system |
| coroutines (`co_await`) | no | no coroutine model (sh2.* has async, but it's a shell-flavored continuation, not typed coroutines) |
| `if consteval` | no | no compile-time evaluation |

**Score:** ~3/40 features expressible (assignment, int-ish variables, MethodCall). Everything else either has no node, or would require smearing (e.g. templates smearing into Declare's init via some opaque blob — defeating the point of having a typed IR).

## 3. Rust feature map

| Feature | shIR expressible? | Why / why not |
|---|---|---|
| `let x = 1;` (immutable) | partial | `Assign` is mutable; no immutability flag |
| `let mut x = 1;` | yes | `Assign` |
| `&x`, `&mut x` (borrows) | no | no reference / borrow concept |
| `&'a T` (lifetimes) | no | no lifetime/region model |
| `String`, `&str` | partial | `Str` is a string; no `String` vs `&str` (owned vs borrowed) distinction; no lifetime parameter |
| `Vec<T>` | partial | `Array` is untyped |
| `Option<T>`, `Result<T,E>` | no | no sum types; no enum-with-data (Rust enums with variants and payloads) |
| `enum Color { Red, Green, Blue }` | no | `Case` is a shell-glob dispatch (`CasePat::Any`, `Substr`); no discriminated union with arbitrary payloads |
| `match x { ... }` | partial | `Case` is match-like but with shell-glob patterns, not Rust patterns; no `Some(x)` / `Ok(x)` binding |
| `trait Foo { fn bar(&self); }` | no | no trait/interface; no `self` semantics; `MethodCall` has a single receiver but no method-set |
| `impl Foo for Bar { ... }` | no | no impl/trait-resolution model |
| `fn foo(x: i32) -> i32 { ... }` | partial | `Function` exists but params are `Vec<String>` (positional, no types); no return type; no generics `<T>` |
| closures `\|x\| x + 1` | no | `Arrow` is a shell-closure (sh2.* body), not a typed Rust closure; no captures, no `Fn`/`FnMut`/`FnOnce` distinction |
| `async fn` / `.await` | no | no async/await model (sh2 has `background` for shell processes, not Rust tasks) |
| `for x in iter { ... }` | partial | `For { var, iter, body }` is iterator-like, but `iter` is a shell-flavored expression (a list of args), not a generic `IntoIterator` |
| `impl Iterator for ...` | no | no trait impls |
| `Box<T>`, `Rc<T>`, `Arc<T>` | no | no pointer/heap-allocation model |
| `&Box<T>`, `Rc<T>::clone()` | no | no ownership/clone |
| `Drop` | no | no destructor / RAII |
| `Send`, `Sync` | no | no thread-safety markers |
| `'static` lifetime | no | no lifetimes |
| `unsafe { ... }` | no | no unsafe-block model |
| `const fn`, `const generics` | no | no compile-time evaluation |
| `where T: Trait` bounds | no | no constraints / trait bounds |
| `dyn Trait` | no | no dynamic dispatch |
| `PhantomData<T>` | no | no zero-sized-type / marker types |
| `Cow<'a, T>` | no | no clone-on-write |
| `Cell<T>`, `RefCell<T>` | no | no interior mutability |
| `Pin<P>` | no | no pinning |
| `async-trait` | no | no async traits |
| `tokio::spawn` | no | no executor model |
| `Result<T, E>` propagation `?` | no | no error-propagation operator |
| ranges `0..n` | partial | `Range { start, end }` exists but only integer ranges; no iterator semantics |
| `impl From<X> for Y` | no | no conversions |
| `derive(Debug, Clone)` | no | no derive / proc-macro model |
| `Pin<&mut T>`, `Unpin` | no | no pinning model |
| async blocks | no | no async-block model |
| `&dyn Trait` | no | no dyn-trait objects |
| `Self` | no | no `Self` type |

**Score:** ~3/40 features expressible (assignment, for-loop, Case/Range). Same shape as C++.

## 4. The fatal gaps — the four structural impossibilities

Beyond the per-feature misses, there are four *structural* things the
shell-domain IR cannot model that any general-purpose language needs:

1. **Types.** shIR is dynamically typed. `Var` carries no type. `Str` /
   `Int` / `Bool` are runtime values, not declarations. There is no
   `let x: i32` and no `Vec<T>`. Even with the `var_types: {Int|Str|Any}`
   annotation (A2), that's a *post-hoc* classification of assignments to
   *shell variables*, not a type system for arbitrary programs. C++ and
   Rust both have type systems as load-bearing features; without one,
   the IR can't be a faithful representation.

2. **References / ownership / borrowing.** shIR has no reference, no
   pointer, no borrow, no lifetime. Every "value" is a string-ish
   first-class thing. C++ references and Rust's borrow checker are the
   *defining* features of each language; lowering them away loses the
   semantics entirely. There's no smearing that preserves them.

3. **Structured control flow with destructuring / pattern binding.**
   shIR's `Case` is a shell-glob dispatch (`*`, `?`, `[abc]`) — the IR
   *cannot* represent Rust's `Some(x) => x + 1` or C++17's
   `std::variant` match. `If` / `While` / `For` exist but don't carry
   bindings. Even the "shape" of a pattern match is a different node
   shape.

4. **Compile-time computation / type-level programming.** C++
   `constexpr` / `if constexpr` / concepts, Rust `const fn` / const
   generics / trait bounds — these run *at compile time* in the source
   language. shIR's optimizer runs at a different stage and with a
   different semantic model; the two are not equivalent. shIR can't
   represent "this expression is evaluated at compile time to produce a
   type."

The other ~70 features (templates, generics, RAII, async, traits, etc.)
are built *on top of* these four, so adding the four would unlock some
of them but not all.

## 5. The design choices, honestly

There are three real choices for C++/Rust sources, in increasing order of
effort and decreasing order of honesty:

### 5a. The verbatim bridge only (status quo, honest about the gap)

A C++/Rust frontend would only lower the *shell-flavored* subset
(assignment, if/while, exec calls, basic arithmetic) and refuse
everything else. The result is a C++/Rust program where 95% of the file
is `// shIR: unsupported` comments. The output, after backend rendering,
is something that *runs* (because all the unsupported bits were
stripped) but doesn't *do what the source said*. **This is a
source-to-shell-filter, not a C++/Rust-to-anything compiler.** It's
fine for a research demo but not for anything a user would expect.

### 5b. A general-purpose layer grafted onto shIR

Add a *second* IR (call it `gpIR`) for C++/Rust with typed variables,
references, lifetimes, structured control flow, pattern matching, etc.
Keep shIR as the shell-specific layer. A C++/Rust source lowers to
gpIR; an "interop" pass translates a shell-flavored subset of gpIR to
shIR for shell execution; the rest stays in gpIR and is rendered by a
C++/Rust-specific backend. This is the cleanest design but it's
**building a second compiler.** Estimated effort: a serious IR with
types, lifetimes, pattern matching, generics — months of design and
years of implementation. It also splits the ecosystem: any new
"universal" language needs both layers, and the interop is the source
of endless bugs.

### 5c. A per-language IR per backend language

For each target language (C++, Rust, …), build a *language-specific* IR
that can represent it. The frontend lowers to that IR. The shIR stays
as a *shell-specific* target. This is essentially what real compilers
do (Clang has its own AST → LLVM IR; rustc has HIR/MIR → LLVM IR). The
shIR isn't the universal hub in this model — LLVM IR (or MIR, or
Cranelift IR) is. **This means shIR's "universal IR" claim is wrong for
non-shell sources**; shIR is the *shell backend's* IR, and the universal
hub is something else (or absent).

### 5d. The honest conclusion

shIR is not adequate for C++/Rust sources as a representation target.
The plan §3 "frontends for new languages" is realistic for *shell-family*
sources (POSIX sh, busybox, fish, zsh-ish — the corpus we have) and
unrealistic for general-purpose sources without a second IR. The
"transitivity" invariant (`e ≈ sh2l(e) ≈ l2js(sh2l(e))`) is *defined*
in shell terms; the test that `bash(e) ≈ sh2perl(e) ≈ perl2js(sh2perl(e))`
passes for the corpus because the corpus IS shell, and the "round-trip"
nets are consistency nets for *conformance to the shell IR*, not a
proof of general-purpose source coverage.

The right architectural response is **5c**: shIR is the shell backend's
IR. The "universal" hub for general-purpose sources is a separate
project (LLVM IR, or a new gpIR). The frontends work I'm doing here
is correctly scoped to the *shell family*; the py2py / C++2sh / Rust2sh
threads from earlier in this thread are real research questions, not
product features, and shouldn't be confused with the shell frontend
work that's product-grade.

## 6. What the plan (§3, §8) is actually good for

The frontends work in this plan is well-scoped to the **shell family**:
POSIX sh, busybox ash, fish, zsh, dash, possibly bash extensions. The
universal IR here means "the IR shared between all shell backends"
(PERL, ESTree/JS, C, C++, Rust, Go as *execution targets for shell*),
not "the IR for arbitrary source languages." That goal is achieved:
the corpus is shell, the IR is shell, the backends render shell. The
frontends under #3 and the testing pyramid under §8 are correct for
this scope.

The C++ and Rust *source* question is a different scope. If pursued
(5b/5c), it's a new project. The honest call: do not add C++/Rust
frontends to this codebase. If someone wants them, they need a
general-purpose IR (5b) or a per-language backend IR (5c) — both of
which are out of scope for shIR as a shell backend IR.
