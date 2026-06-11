package server

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/auth"
	sdkconfig "github.com/anyproto/any-sync-sdk/config"
	"github.com/anyproto/any-sync-sdk/handler"
	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/agentdebug"
	"github.com/anyproto/any/internal/agentlog"
	"github.com/anyproto/any/internal/agentmem"
	"github.com/anyproto/any/internal/chat"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/editor"
	"github.com/anyproto/any/internal/miniapp"
	"github.com/anyproto/any/internal/nav"
	"github.com/anyproto/any/internal/program"
)

// logConfigOnce gates Config.ApplyGlobal so the zap defaults are set
// at most once per process. ApplyGlobal mutates the
// any-sync/app/logger package globals (SetDefault, SetNamedLevels),
// which races against any background SDK goroutine still reading the
// logger after a prior boot — in the test suite, newTestDeps creates
// + tears down the SDK per test, and the streampool sendLoop's Debug
// calls from the previous boot trip the detector when the next boot
// re-applies the config.
//
// In production Run calls ApplyGlobal first; OpenSDK's call is a
// belt-and-braces no-op after the Once fires. In tests the first
// newTestDeps wins; subsequent ones leave the global alone.
var logConfigOnce sync.Once

// OpenSDK boots the SDK against the wallet provider and the project
// config. Storage lives under <dataDir>/sdk so the SDK's any-store and
// any-sync state are isolated from other process state in the data dir.
//
// Topology defaults to Shared; only "shared" is supported in v1
// (per-space topology is on the SDK side but not yet exercised here).
func OpenSDK(ctx context.Context, cfg config.Config, dataDir string, provider auth.Provider) (*anysyncsdk.SDK, error) {
	nodeconfYAML, nodeconfSource, err := config.LoadNodeconf(cfg.Network)
	if err != nil {
		return nil, err
	}

	topology := sdkconfig.StorageShared
	switch cfg.Storage.Topology {
	case "", "shared":
		topology = sdkconfig.StorageShared
	case "per-space":
		topology = sdkconfig.StoragePerSpace
	default:
		return nil, fmt.Errorf("unknown storage topology %q", cfg.Storage.Topology)
	}

	logConfigOnce.Do(cfg.Log.ApplyGlobal)

	// Which nodeconf source won is the one boot fact that separates
	// "configured network" from "silently on staging" (see the
	// NodeconfSource* doc in internal/config/nodeconf.go).
	logger.NewNamed("server").Info("nodeconf",
		zap.String("source", nodeconfSource),
		zap.String("networkId", config.NodeconfNetworkID(nodeconfYAML)))

	sdkCfg := sdkconfig.Config{
		Storage: sdkconfig.Storage{
			DataDir:  filepath.Join(dataDir, "sdk"),
			Topology: topology,
		},
		Network: sdkconfig.Network{NodeConfYAML: nodeconfYAML},
		Sync:    sdkconfig.Sync{ChangeBatchSize: cfg.Sync.ChangeBatchSize},
		// Hardcoded types this server adds on top of the SDK's
		// built-ins. Each entry registers its handler(s) with every
		// per-object Controller, so writes targeting the type's
		// dataset(s) flow through the type's validation logic.
		Types: []handler.Type{
			editor.NewType(),
			chat.NewType(),
			program.NewType(),
			miniapp.NewType(),
			agentdebug.NewType(),
			agentlog.NewType(), // agent_turns + agent_chunks on the chat object
			agentmem.NewType(), // agent_memory_items on the per-space brain object
			nav.NewType(),      // property-only: no dataset, just nav.* schema
		},
	}
	if cfg.Sync.DialTimeout != "" {
		d, err := time.ParseDuration(cfg.Sync.DialTimeout)
		if err != nil {
			return nil, fmt.Errorf("parse sync.dialTimeout %q: %w", cfg.Sync.DialTimeout, err)
		}
		sdkCfg.Sync.DialTimeout = d
	}

	return anysyncsdk.Open(ctx, sdkCfg, provider)
}
