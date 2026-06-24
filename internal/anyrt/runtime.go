package anyrt

import (
	"github.com/anyproto/anytype-agent-runtime/anyruntime"
	agentruntime "github.com/anyproto/anytype-agent-runtime/runtime"
	"github.com/anyproto/anytype-agent-runtime/runtime/hostfn"
)

type RuntimeConfig struct {
	APIBaseURL     string
	SpaceID        string
	PrivateSpaceID string
	ProgramTypeID  string
	// DebugFolderID is the "Debug" nav folder the agent files its
	// "Agent Debug Log" pages under (exposed to JS as
	// env.ANY_DEBUG_FOLDER_ID). Empty leaves debug pages at root.
	DebugFolderID string
	// ExtraLoaders are chained AFTER the anySDK loader — the space stays
	// the source of truth (live reload: a program edit is picked up on the
	// next import); files only fill misses (test-local modules).
	ExtraLoaders []anyruntime.ModuleLoader
	// ExtraEnv is merged into the JS-visible `env` map after the standard
	// keys (so a dotenv file can add e.g. provider API keys).
	ExtraEnv map[string]string
}

// SetupAnySDKDirtyRuntime configures a fresh runtime against the `any`
// server: standard effects (fetch/fetchBatch/sleep), console, js.eval, trace
// wrapping, the env map, and module resolution through NewAnySDKLoader (every
// resolve is recorded as a `module.resolve` trace entry). Chat replies are not
// a runtime effect — programs post through the native chat API
// (anyHelper.sendChatMessage).
func SetupAnySDKDirtyRuntime(rt agentruntime.Runtime, cfg RuntimeConfig) {
	rt.SetEffectResolver("fetch", hostfn.Fetch)
	rt.SetEffectResolver("fetchBatch", hostfn.FetchBatch)
	rt.SetEffectResolver("sleep", hostfn.Sleep)
	rt.EnableConsole()
	rt.EnableJSEval()
	rt.EnableWrapTrace()

	rt.SetGlobal("cosineSimilarity", func(a, b []float64) float64 {
		return hostfn.CosineSimilarity(a, b)
	})
	rt.SetGlobal("cosineSimilarityHex", func(hexA, hexB string) float64 {
		return hostfn.CosineSimilarityHex(hexA, hexB)
	})

	privateSpaceID := cfg.PrivateSpaceID
	if privateSpaceID == "" {
		privateSpaceID = cfg.SpaceID
	}
	envMap := map[string]any{
		"ANY_API_URL":          cfg.APIBaseURL,
		"ANY_SPACE_ID":         cfg.SpaceID,
		"ANY_PRIVATE_SPACE_ID": privateSpaceID,
		"ANY_DEBUG_FOLDER_ID":  cfg.DebugFolderID,
		// Anytype compat — assistantjs reads these in init_agent.js
		"ANYTYPE_API_URL":          cfg.APIBaseURL,
		"ANYTYPE_API_KEY":          "",
		"ANYTYPE_SPACE_ID":         cfg.SpaceID,
		"ANYTYPE_PRIVATE_SPACE_ID": privateSpaceID,
	}
	for k, v := range cfg.ExtraEnv {
		if _, reserved := envMap[k]; !reserved {
			envMap[k] = v
		}
	}
	rt.SetGlobal("env", envMap)

	loaders := []anyruntime.ModuleLoader{
		NewAnySDKLoader(LoaderConfig{
			BaseURL:        cfg.APIBaseURL,
			SpaceID:        cfg.SpaceID,
			PrivateSpaceID: privateSpaceID,
			ProgramTypeID:  cfg.ProgramTypeID,
			OnResolve: func(info anyruntime.ResolveInfo) {
				rt.RecordTraceEntry("module.resolve", info.ImportString, map[string]any{
					"importString":    info.ImportString,
					"currentSpaceId":  info.CurrentSpaceID,
					"resolvedSpaceId": info.ResolvedSpaceID,
					"fallback":        info.Fallback,
				})
			},
		}),
	}
	loaders = append(loaders, cfg.ExtraLoaders...)
	rt.SetModuleResolver(anyruntime.ChainLoaders(loaders...))
}
