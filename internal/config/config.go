package config

import "github.com/anyproto/any-sync/app/logger"

type Config struct {
	DataDir string  `yaml:"dataDir"`
	Listen  Listen  `yaml:"listen"`
	Auth    Auth    `yaml:"auth"`
	Network Network `yaml:"network"`
	Storage Storage `yaml:"storage"`
	Sync    Sync    `yaml:"sync"`
	Log     logger.Config `yaml:"log"`
}

type Listen struct {
	Addr string `yaml:"addr"`
}

type Auth struct {
	WalletPath string `yaml:"walletPath"`
	PasskeyEnv string `yaml:"passkeyEnv"`
}

type Network struct {
	NodeconfPath string `yaml:"nodeconfPath"`
	Nodeconf     string `yaml:"nodeconf"`
}

type Storage struct {
	Topology string `yaml:"topology"`
}

type Sync struct {
	DialTimeout     string `yaml:"dialTimeout"`
	ChangeBatchSize int    `yaml:"changeBatchSize"`
}

// Defaults returns a Config populated with v1 defaults. Paths here are
// unexpanded — Load resolves them against the process environment.
func Defaults() Config {
	return Config{
		DataDir: "~/.any",
		Listen:  Listen{Addr: "127.0.0.1:7001"},
		Auth:    Auth{PasskeyEnv: "ANY_WALLET_PASSKEY"},
		Storage: Storage{Topology: "shared"},
		Log: logger.Config{
			DefaultLevel: "info",
			Format:       logger.ColorizedOutput,
		},
	}
}
