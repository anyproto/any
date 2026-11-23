package server

import (
	"fmt"
	"io"
)

// PrintMnemonic writes the first-run warning and BIP-39 phrase to w, the
// command's stderr so it doesn't contaminate piped output. A write that
// fails loses the only copy shown: its error is returned.
func PrintMnemonic(w io.Writer, phrase string) error {
	const rule = "========================================================================"
	_, err := fmt.Fprintf(w, "%s\n%s\n%s\n%s\n%s\n%s\n", rule,
		"  A new wallet was generated. Write down the recovery phrase below.",
		"  You will NOT see it again. Anyone with this phrase owns the account.",
		rule, phrase, rule)
	return err
}
