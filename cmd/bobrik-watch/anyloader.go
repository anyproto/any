package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/anyproto/anytype-agent-runtime/anyruntime"
)

type anySDKLoaderConfig struct {
	BaseURL        string
	SpaceID        string
	PrivateSpaceID string
	ProgramTypeID  string
	OnResolve      func(anyruntime.ResolveInfo)
}

func newAnySDKLoader(cfg anySDKLoaderConfig) anyruntime.ModuleLoader {
	lookup := newAnySDKLookup(cfg.BaseURL, cfg.ProgramTypeID)

	return func(importString string) (string, error) {
		spaceID, progName, progVersion, qualified, err := parseModuleName(cfg, importString)
		if err != nil {
			return "", err
		}

		source, found, err := lookup(spaceID, progName, progVersion)
		if err != nil {
			return "", fmt.Errorf("module %q: %w", importString, err)
		}
		if found {
			emitResolve(cfg, anyruntime.ResolveInfo{
				ImportString:    importString,
				CurrentSpaceID:  spaceID,
				ResolvedSpaceID: spaceID,
				Fallback:        false,
			})
			return source, nil
		}

		canFallback := !qualified && cfg.PrivateSpaceID != "" && cfg.PrivateSpaceID != spaceID
		if !canFallback {
			emitResolve(cfg, anyruntime.ResolveInfo{
				ImportString:   importString,
				CurrentSpaceID: spaceID,
			})
			return "", fmt.Errorf("module %q not found in space %s", importString, spaceID)
		}

		source, found, err = lookup(cfg.PrivateSpaceID, progName, progVersion)
		if err != nil {
			return "", fmt.Errorf("module %q (private fallback): %w", importString, err)
		}
		if found {
			emitResolve(cfg, anyruntime.ResolveInfo{
				ImportString:    importString,
				CurrentSpaceID:  spaceID,
				ResolvedSpaceID: cfg.PrivateSpaceID,
				Fallback:        true,
			})
			return source, nil
		}

		emitResolve(cfg, anyruntime.ResolveInfo{
			ImportString:   importString,
			CurrentSpaceID: spaceID,
		})
		return "", fmt.Errorf("module %q not found in space %s nor in private space %s", importString, spaceID, cfg.PrivateSpaceID)
	}
}

func parseModuleName(cfg anySDKLoaderConfig, raw string) (spaceID, progName, progVersion string, qualified bool, err error) {
	if idx := strings.Index(raw, ":"); idx >= 0 {
		prefix := raw[:idx]
		rest := raw[idx+1:]
		if !strings.Contains(prefix, "@") {
			if prefix == "private" {
				spaceID = cfg.PrivateSpaceID
			} else {
				spaceID = prefix
			}
			qualified = true
			raw = rest
		}
	}
	if spaceID == "" {
		spaceID = cfg.SpaceID
	}
	parts := strings.SplitN(raw, "@", 2)
	if len(parts) != 2 {
		return "", "", "", false, fmt.Errorf("module %q: expected name@version format", raw)
	}
	return spaceID, parts[0], parts[1], qualified, nil
}

func emitResolve(cfg anySDKLoaderConfig, info anyruntime.ResolveInfo) {
	if cfg.OnResolve != nil {
		cfg.OnResolve(info)
	}
}

// newAnySDKLookup builds the leaf lookup that queries the any API for program objects.
// Programs are objects of the given type with name and version properties.
func newAnySDKLookup(baseURL, programTypeID string) func(spaceID, progName, progVersion string) (string, bool, error) {
	return func(spaceID, progName, progVersion string) (string, bool, error) {
		objectID, err := findProgramObject(baseURL, spaceID, programTypeID, progName, progVersion)
		if err != nil {
			return "", false, err
		}
		if objectID == "" {
			return "", false, nil
		}

		code, err := queryProgramSource(baseURL, spaceID, objectID)
		if err != nil {
			return "", false, fmt.Errorf("reading source for %s: %w", objectID, err)
		}
		if code == "" {
			return "", false, fmt.Errorf("empty program_source in object %s (space %s)", objectID, spaceID)
		}
		return code, true, nil
	}
}

// findProgramObject queries for a program object by its typed properties.
func findProgramObject(baseURL, spaceID, programTypeID, progName, progVersion string) (string, error) {
	filter := map[string]any{
		"filter": map[string]any{
			programTypeID + ".name":    progName,
			programTypeID + ".version": progVersion,
		},
	}
	body, _ := json.Marshal(filter)
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/query",
		"application/json",
		strings.NewReader(string(body)),
	)
	if err != nil {
		return "", fmt.Errorf("query programs in space %s: %w", spaceID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("query programs: %d %s", resp.StatusCode, msg)
	}

	var out struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode query response: %w", err)
	}
	if len(out.Records) == 0 {
		return "", nil
	}
	var rec struct {
		Id string `json:"id"`
	}
	if err := json.Unmarshal(out.Records[0], &rec); err != nil {
		return "", fmt.Errorf("decode record: %w", err)
	}
	return rec.Id, nil
}

// queryProgramSource reads the "code" field from the program_source dataset.
func queryProgramSource(baseURL, spaceID, objectID string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"objectId": objectID,
		"dataset":  "program_source",
	})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/query",
		"application/json",
		strings.NewReader(string(body)),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("query program_source: %d %s", resp.StatusCode, msg)
	}
	var out struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Records) == 0 {
		return "", nil
	}
	var rec struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out.Records[0], &rec); err != nil {
		return "", err
	}
	return rec.Code, nil
}
