package main

import "fmt"

// t119_local_slot_isolation: two functions that each declare a same-named
// local slice must get SEPARATE storage, and a call must not clobber the
// caller's accumulated items. `var words []string` lowers to
// setArray("words", …) and every read lowers to the same bare name, so
// without a function-unique store name (`words__7`) the callee's reset
// wipes the caller's list and a() prints the callee's contents.
// Pins scopeLocalStoreName/resolveVar/localModel (DOGFOOD.md, gap 2).
func b() {
	var words []string
	words = append(words, "B", "C")
	fmt.Println("b", len(words))
}

func a() {
	var words []string
	words = append(words, "A")
	b()
	fmt.Println("a", len(words), words[0])
}

func main() {
	a()
	b()
}
