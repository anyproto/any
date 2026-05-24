package main

import (
	"os"

	"github.com/anyproto/anytype-agent-runtime/anyruntime"
	agentrt "github.com/anyproto/anytype-agent-runtime/runtime"
	"github.com/anyproto/anytype-agent-runtime/runtime/hostfn"
)

type AnySDKRuntimeConfig struct {
	APIBaseURL     string
	SpaceID        string
	PrivateSpaceID string
	ProgramTypeID  string
}

func SetupAnySDKDirtyRuntime(rt agentrt.Runtime, cfg AnySDKRuntimeConfig) {
	rt.SetEffectResolver("fetch", hostfn.Fetch)
	rt.SetEffectResolver("fetchBatch", hostfn.FetchBatch)
	rt.SetEffectResolver("sleep", hostfn.Sleep)
	rt.SetEffectResolver("chatReply", hostfn.NewChatReply(os.Stdout))
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
		// Anytype compat — assistantjs reads these in init_agent.js
		"ANYTYPE_API_URL":          cfg.APIBaseURL,
		"ANYTYPE_API_KEY":          "",
		"ANYTYPE_SPACE_ID":         cfg.SpaceID,
		"ANYTYPE_PRIVATE_SPACE_ID": privateSpaceID,
	}
	rt.SetGlobal("env", envMap)

	rt.SetModuleResolver(anyruntime.ChainLoaders(
		newAnySDKLoader(anySDKLoaderConfig{
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
	))
}
