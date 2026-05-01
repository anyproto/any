package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// ResolvePasskey sources the wallet passkey from the environment variable
// named by cfg.Auth.PasskeyEnv, or from stdin when fromStdin is true.
// Returns an empty string when neither source is configured — the caller
// treats that as a request for a plain wallet.
func ResolvePasskey(cfg Config, fromStdin bool) (string, error) {
	if fromStdin {
		return readSingleLine(os.Stdin)
	}
	envName := cfg.Auth.PasskeyEnv
	if envName == "" {
		envName = "ANY_WALLET_PASSKEY"
	}
	return os.Getenv(envName), nil
}

func readSingleLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read passkey: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
