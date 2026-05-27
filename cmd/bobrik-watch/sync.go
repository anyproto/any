package main

import (
	"bytes"
	"embed"
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

//go:embed skills/*
var skillsFS embed.FS

//go:embed tool-descriptions/*
var toolDescFS embed.FS

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
		{"xKey": "source", "name": "source", "kind": "string"},
		{"xKey": "tool_description", "name": "tool_description", "kind": "string"},
		{"xKey": "tool_schema", "name": "tool_schema", "kind": "string"},
	} {
		if err := addProperty(baseURL, spaceID, typeID, prop); err != nil {
			return "", fmt.Errorf("add property %s: %w", prop["xKey"], err)
		}
	}
	return typeID, nil
}

// ensureSkillType creates the Agent Skill type with __any_agent_skill_name
// property if it doesn't already exist. Returns the type ID.
func ensureSkillType(baseURL, spaceID string) (string, error) {
	typeID, err := findType(baseURL, spaceID, "Agent Skill")
	if err != nil {
		return "", err
	}
	if typeID != "" {
		return typeID, nil
	}

	body, _ := json.Marshal(map[string]string{"name": "Agent Skill"})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/types",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("create skill type: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create skill type: %d %s", resp.StatusCode, msg)
	}
	var created struct {
		TypeId string `json:"typeId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("decode created type: %w", err)
	}
	typeID = created.TypeId
	fmt.Fprintf(os.Stderr, "created type \"Agent Skill\" → %s\n", typeID)

	if err := addProperty(baseURL, spaceID, typeID, map[string]string{
		"xKey": "__any_agent_skill_name", "name": "__any_agent_skill_name", "kind": "string",
	}); err != nil {
		return "", fmt.Errorf("add skill name property: %w", err)
	}
	return typeID, nil
}

// syncSkills reads embedded skill .md files and upserts them as Agent Skill
// objects. Each skill is identified by __any_agent_skill_name (e.g. "_soul").
// Content is stored via PUT /editor/markdown.
func syncSkills(baseURL, spaceID, skillTypeID, folderID string) error {
	entries, err := skillsFS.ReadDir("skills")
	if err != nil {
		return fmt.Errorf("read embedded skills: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		skillName := strings.TrimSuffix(e.Name(), ".md")
		content, err := skillsFS.ReadFile("skills/" + e.Name())
		if err != nil {
			return fmt.Errorf("read skill %s: %w", e.Name(), err)
		}

		objectID, err := findSkillObject(baseURL, spaceID, skillTypeID, skillName)
		if err != nil {
			return fmt.Errorf("query skill %s: %w", skillName, err)
		}

		if objectID != "" {
			if err := setObjectMarkdown(baseURL, spaceID, objectID, string(content)); err != nil {
				return fmt.Errorf("update skill %s: %w", skillName, err)
			}
			fmt.Fprintf(os.Stderr, "synced skill %s (updated %s)\n", skillName, objectID)
		} else {
			objectID, err = createSkillObject(baseURL, spaceID, skillTypeID, skillName, string(content))
			if err != nil {
				return fmt.Errorf("create skill %s: %w", skillName, err)
			}
			fmt.Fprintf(os.Stderr, "synced skill %s (created %s)\n", skillName, objectID)
		}

		if folderID != "" {
			_ = setNavParent(baseURL, spaceID, objectID, folderID)
		}
	}
	return nil
}

func findSkillObject(baseURL, spaceID, skillTypeID, skillName string) (string, error) {
	filter := map[string]any{
		"filter": map[string]any{
			skillTypeID + ".__any_agent_skill_name": skillName,
		},
	}
	body, _ := json.Marshal(filter)
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/query",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
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
		Id string `json:"id"`
	}
	if err := json.Unmarshal(out.Records[0], &rec); err != nil {
		return "", err
	}
	return rec.Id, nil
}

func createSkillObject(baseURL, spaceID, skillTypeID, skillName, markdown string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"types": []string{skillTypeID},
		"initialProperties": map[string]any{
			"any": map[string]any{
				"name": "Skill: " + skillName,
			},
			skillTypeID: map[string]any{
				"__any_agent_skill_name": skillName,
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
		return "", fmt.Errorf("create skill: %d %s", resp.StatusCode, msg)
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
	body, _ := json.Marshal(map[string]string{"content": markdown})
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

// toolDescription returns the tool description for a program name,
// read from embedded tool-descriptions/*.md files. Returns "" if
// no description file exists for the given name.
func toolDescription(name string) string {
	data, err := toolDescFS.ReadFile("tool-descriptions/" + name + ".md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// syncPrograms reads .js files from dir and upserts them as program
// objects in the given space.
//
// Filename convention: "name@version.js" → name="name", version="version".
// Files without @version default to "v1".
//
// skipNames lists filenames (without .js) to skip (e.g. "anytypeHelper").
//
// The embedded anyHelper.js is always synced as anyHelper@v1.
func syncPrograms(baseURL, spaceID, programTypeID, dir string, skipNames map[string]bool, folderID string) error {
	// Sync the embedded anyHelper.js first
	if err := upsertProgram(baseURL, spaceID, programTypeID, "anyHelper", "v1", anyHelperJS, folderID); err != nil {
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

		if err := upsertProgram(baseURL, spaceID, programTypeID, name, version, string(source), folderID); err != nil {
			return fmt.Errorf("sync %s@%s: %w", name, version, err)
		}
	}
	return nil
}

func upsertProgram(baseURL, spaceID, programTypeID, name, version, source, folderID string) error {
	objectID, err := findProgramObject(baseURL, spaceID, programTypeID, name, version)
	if err != nil {
		return fmt.Errorf("query %s@%s: %w", name, version, err)
	}

	if objectID != "" {
		if err := modifyDataset(baseURL, spaceID, objectID, "program_source", "main", map[string]any{"code": source}); err != nil {
			return fmt.Errorf("update %s@%s: %w", name, version, err)
		}
		fmt.Fprintf(os.Stderr, "synced %s@%s (updated %s)\n", name, version, objectID)
	} else {
		objectID, err = createProgramObject(baseURL, spaceID, programTypeID, name, version, source)
		if err != nil {
			return fmt.Errorf("create %s@%s: %w", name, version, err)
		}
		fmt.Fprintf(os.Stderr, "synced %s@%s (created %s)\n", name, version, objectID)
	}

	if folderID != "" {
		_ = setNavParent(baseURL, spaceID, objectID, folderID)
	}

	if desc := toolDescription(name); desc != "" {
		_ = modifyDataset(baseURL, spaceID, objectID, "program_description", "main", map[string]any{"text": desc})
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

func createProgramObject(baseURL, spaceID, programTypeID, name, version, source string) (string, error) {
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

	if err := modifyDataset(baseURL, spaceID, obj.ObjectId, "program_source", "main", map[string]any{"code": source}); err != nil {
		return "", fmt.Errorf("set source: %w", err)
	}
	return obj.ObjectId, nil
}

// modifyDataset upserts a single record in a dataset on an object.
func modifyDataset(baseURL, spaceID, objectID, dataset, recordID string, value map[string]any) error {
	body, _ := json.Marshal(map[string]any{
		"objectId": objectID,
		"dataset":  dataset,
		"records": []map[string]any{{
			"id":     recordID,
			"upsert": true,
			"ops": []map[string]any{{
				"type":  "$set",
				"path":  "",
				"value": value,
			}},
		}},
	})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/modify",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("modify %s: %d %s", dataset, resp.StatusCode, msg)
	}
	return nil
}

const systemFolderName = "System Bobrik Files"

func ensureSystemFolder(baseURL, spaceID string) (string, error) {
	filter := map[string]any{
		"filter": map[string]any{
			"any.name": systemFolderName,
			"nav.type": 2,
		},
	}
	body, _ := json.Marshal(filter)
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/query",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Records []struct {
			Id string `json:"id"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Records) > 0 {
		return out.Records[0].Id, nil
	}

	createBody, _ := json.Marshal(map[string]any{
		"nav":               map[string]any{"type": 2},
		"initialProperties": map[string]any{"any": map[string]any{"name": systemFolderName}},
	})
	resp2, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects",
		"application/json",
		bytes.NewReader(createBody),
	)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp2.Body)
		return "", fmt.Errorf("create system folder: %d %s", resp2.StatusCode, msg)
	}
	var obj struct {
		ObjectId string `json:"objectId"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&obj); err != nil {
		return "", err
	}
	return obj.ObjectId, nil
}

func setNavParent(baseURL, spaceID, objectID, parentID string) error {
	body, _ := json.Marshal(map[string]any{
		"patch": map[string]any{"parentId": parentID},
	})
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/properties/"+url.PathEscape(objectID)+"/base/nav",
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
		return fmt.Errorf("set nav parent: %d %s", resp.StatusCode, msg)
	}
	return nil
}
