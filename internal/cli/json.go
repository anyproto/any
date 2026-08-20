package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// readJSONBody decodes a request body from --json (inline string or
// @file / - for stdin). Complex nested bodies are JSON-first in the
// CLI; common scalar flags would explode into dozens of options
// without adding clarity.
func readJSONBody(raw string, out any) error {
	var buf []byte
	switch {
	case raw == "":
		return fmt.Errorf("supply --json BODY (inline JSON, @FILE, or - for stdin)")
	case raw == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		buf = b
	case raw[0] == '@':
		b, err := os.ReadFile(raw[1:])
		if err != nil {
			return fmt.Errorf("read %s: %w", raw[1:], err)
		}
		buf = b
	default:
		buf = []byte(raw)
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return fmt.Errorf("parse body: %w", err)
	}
	return nil
}
