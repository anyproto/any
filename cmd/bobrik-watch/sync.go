package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

//go:embed anyHelper.js
var anyHelperJS string

// ensureProgramType creates the Program type with name and version
// properties if it doesn't already exist. Returns the type ID.
func ensureProgramType(baseURL, spaceID string) (string, error) {
	typeID, err := findType(baseURL, spaceID, "Program")
	if err != nil {
		return "", err
	}
	if typeID != "" {
		return typeID, nil
	}

	body, _ := json.Marshal(map[string]string{
		"name": "Program",
	})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/types",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("create type: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create type: %d %s", resp.StatusCode, msg)
	}
	var created struct {
		TypeId string `json:"typeId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("decode created type: %w", err)
	}
	typeID = created.TypeId
	fmt.Fprintf(os.Stderr, "created type \"Program\" → %s\n", typeID)

	for _, prop := range []map[string]string{
		{"xKey": "name", "name": "name", "kind": "string"},
		{"xKey": "version", "name": "version", "kind": "string"},
	} {
		if err := addProperty(baseURL, spaceID, typeID, prop); err != nil {
			return "", fmt.Errorf("add property %s: %w", prop["xKey"], err)
		}
	}
	return typeID, nil
}

func findType(baseURL, spaceID, typeName string) (string, error) {
	resp, err := http.Get(baseURL + "/v1/spaces/" + url.PathEscape(spaceID) + "/types")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Types []struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"types"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	for _, t := range out.Types {
		if t.Name == typeName {
			return t.Id, nil
		}
	}
	return "", nil
}

func addProperty(baseURL, spaceID, typeID string, prop map[string]string) error {
	body, _ := json.Marshal(prop)
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/types/"+url.PathEscape(typeID)+"/properties",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%d %s", resp.StatusCode, msg)
	}
	return nil
}

// syncPrograms reads .js files from dir and upserts them as any_program
// objects in the given space. Each file becomes one program object with
// its source wrapped in a markdown code fence with // __main_source.
//
// Filename convention: "name@version.js" → name="name", version="version".
// Files without @version default to "v1".
//
// skipNames lists filenames (without .js) to skip (e.g. "anytypeHelper").
//
// The embedded anyHelper.js is always synced as anyHelper@v1.
func syncPrograms(baseURL, spaceID, programTypeID, dir string, skipNames map[string]bool) error {
	// Sync the embedded anyHelper.js first
	if err := upsertProgram(baseURL, spaceID, programTypeID, "anyHelper", "v1", anyHelperJS); err != nil {
		return fmt.Errorf("sync anyHelper: %w", err)
	}
	fmt.Fprintf(os.Stderr, "synced anyHelper@v1 (embedded)\n")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		baseName := strings.TrimSuffix(e.Name(), ".js")
		if skipNames[baseName] {
			continue
		}

		name, version := parseProgramFilename(baseName)

		source, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}

		if err := upsertProgram(baseURL, spaceID, programTypeID, name, version, string(source)); err != nil {
			return fmt.Errorf("sync %s@%s: %w", name, version, err)
		}
	}
	return nil
}

func upsertProgram(baseURL, spaceID, programTypeID, name, version, source string) error {
	markdown := wrapSourceAsMarkdown(source)

	objectID, err := findProgramObject(baseURL, spaceID, programTypeID, name, version)
	if err != nil {
		return fmt.Errorf("query %s@%s: %w", name, version, err)
	}

	if objectID != "" {
		if err := setObjectMarkdown(baseURL, spaceID, objectID, markdown); err != nil {
			return fmt.Errorf("update %s@%s: %w", name, version, err)
		}
		fmt.Fprintf(os.Stderr, "synced %s@%s (updated %s)\n", name, version, objectID)
	} else {
		id, err := createProgramObject(baseURL, spaceID, programTypeID, name, version, markdown)
		if err != nil {
			return fmt.Errorf("create %s@%s: %w", name, version, err)
		}
		fmt.Fprintf(os.Stderr, "synced %s@%s (created %s)\n", name, version, id)
	}
	return nil
}

// parseProgramFilename splits "name@version" → (name, version).
// If no @version, defaults to "v1".
func parseProgramFilename(baseName string) (name, version string) {
	if idx := strings.LastIndex(baseName, "@"); idx >= 0 {
		return baseName[:idx], baseName[idx+1:]
	}
	return baseName, "v1"
}

func wrapSourceAsMarkdown(source string) string {
	return "```js\n// __main_source\n" + source + "\n```\n"
}

func createProgramObject(baseURL, spaceID, programTypeID, name, version, markdown string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"types": []string{programTypeID},
		"initialProperties": map[string]any{
			"any": map[string]any{
				"name": name + "@" + version,
			},
			programTypeID: map[string]any{
				"name":    name,
				"version": version,
			},
		},
	})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create object: %d %s", resp.StatusCode, msg)
	}
	var obj struct {
		ObjectId string `json:"objectId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return "", err
	}

	if err := setObjectMarkdown(baseURL, spaceID, obj.ObjectId, markdown); err != nil {
		return "", fmt.Errorf("set markdown: %w", err)
	}
	return obj.ObjectId, nil
}

func setObjectMarkdown(baseURL, spaceID, objectID, markdown string) error {
	body, _ := json.Marshal(map[string]string{"markdown": markdown})
	req, err := http.NewRequest(http.MethodPut,
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/"+url.PathEscape(objectID)+"/editor/markdown",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set markdown: %d %s", resp.StatusCode, msg)
	}
	return nil
}
