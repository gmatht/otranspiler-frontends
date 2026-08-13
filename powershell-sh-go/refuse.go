package ps1lib

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
)

// refuse — the REFUSE > GUESS contract: anything outside the v1 subset
// must fail the emit loudly, never miscompile. The message names the
// construct and, where possible, the offending text.
func refuse(n *sitter.Node, src []byte, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if n != nil {
		if t := n.Content(src); t != "" {
			msg += fmt.Sprintf(" near %q", t)
		}
	}
	return fmt.Errorf("REFUSE: %s (PLAN_POWERSHELL_F.md v1 subset — refuse > guess)", msg)
}
