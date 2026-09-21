package config

import "runtime"

// LocalDiscoveryEnabled resolves p2p.localDiscovery. An explicit value
// wins. Absent, a managed server on an Apple platform starts with
// discovery off: the Local Network prompt fires on the first multicast
// send, and the host shell owns that permission flow, so it turns
// discovery on through PUT /v1/local-discovery once the user has
// answered. Every other setup starts on.
func (c Config) LocalDiscoveryEnabled() bool {
	if c.P2P.LocalDiscovery != nil {
		return *c.P2P.LocalDiscovery
	}
	return !(c.Managed() && applePlatform())
}

// applePlatform reports a build for macOS or iOS, the platforms with a
// Local Network permission prompt.
func applePlatform() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "ios"
}
