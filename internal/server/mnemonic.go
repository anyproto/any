package server

import (
	"fmt"
	"os"
)

// PrintMnemonic writes the first-run warning and BIP-39 phrase to stderr.
// Stderr (not stdout) so it doesn't contaminate piped output, and only on
// the wallet's first generation.
func PrintMnemonic(phrase string) {
	fmt.Fprintln(os.Stderr, "========================================================================")
	fmt.Fprintln(os.Stderr, "  A new wallet was generated. Write down the recovery phrase below.")
	fmt.Fprintln(os.Stderr, "  You will NOT see it again. Anyone with this phrase owns the account.")
	fmt.Fprintln(os.Stderr, "========================================================================")
	fmt.Fprintln(os.Stderr, phrase)
	fmt.Fprintln(os.Stderr, "========================================================================")
}
