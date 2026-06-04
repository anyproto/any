package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// All three are derived from filepath.Dir(programsDir) at boot so SIGHUP
// refresh re-reads the live files on disk — embedding defeated the whole
// point of the bootstrap/refresh story (anyHelper.js edits stayed pinned
// to the binary timestamp).
var (
	anyHelperPath       string // <bobrikDir>/anyHelper.js
	skillsDir           string // <bobrikDir>/skills
	toolDescriptionsDir string // <bobrikDir>/tool-descriptions
)

// ensureProgramType creates the Program type with name and version
// properties if it doesn't already exist. Returns the type ID.
func ensureProgramType(baseURL, spaceID string) (string, error) {
	// Existence by xKey, not name. The builtin `program` type carries
	// xKey="program" (server reports xKey=id for builtins), so this resolves to
	// it; only a space lacking it falls through to create.
	typeID, err := findTypeByXKey(baseURL, spaceID, "program")
	if err != nil {
		return "", err
	}
	if typeID != "" {
		return typeID, nil
	}

	body, _ := json.Marshal(map[string]string{
		"name": "Program",
		"xKey": "program",
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

// skillNameXKey is the stable property key for a skill's name. Bare (no
// legacy __any_ prefix) — the type namespace already scopes it. The server
// stores property values under the derived propId, not the xKey, so callers
// resolve xKey→propId via skillNamePropID before reading/writing/filtering.
const skillNameXKey = "agent_skill_name"

// ensureSkillType ensures the "Agent Skill" type exists AND carries the
// agent_skill_name property (find-or-add — idempotent and additive, so it
// composes with init_agent's declaration of the same type). Returns the type ID.
func ensureSkillType(baseURL, spaceID string) (string, error) {
	// Existence by xKey, not name — see findTypeByXKey. A pre-xKey "Agent Skill"
	// type (no xKey) won't match here, so we create a correct xKey-bearing one
	// and the agent can resolve "agent_skill"; the stale type orphans.
	typeID, err := findTypeByXKey(baseURL, spaceID, "agent_skill")
	if err != nil {
		return "", err
	}
	if typeID == "" {
		// xKey must match what the JS reader uses (toolcall_core reads
		// "agent_skill.agent_skill_name"); set it explicitly so it's stable
		// regardless of whether init_agent or this bootstrap creates the type
		// first (xKey is first-create-wins). Matches anyHelper's name→xKey slug.
		body, _ := json.Marshal(map[string]string{"name": "Agent Skill", "xKey": "agent_skill"})
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
	}

	// Ensure the name property exists (the type may have been created by
	// init_agent, or pre-exist without it). addProperty errors if it's already
	// there — tolerate that by checking first.
	propID, err := skillNamePropID(baseURL, spaceID, typeID)
	if err != nil {
		return "", err
	}
	if propID == "" {
		if err := addProperty(baseURL, spaceID, typeID, map[string]string{
			"xKey": skillNameXKey, "name": skillNameXKey, "kind": "string",
		}); err != nil {
			return "", fmt.Errorf("add skill name property: %w", err)
		}
	}
	return typeID, nil
}

// syncSkills reads skill .md files from skillsDir on disk and upserts
// them as Agent Skill objects. Each skill is identified by
// agent_skill_name (e.g. "_soul"). Content is stored via PUT
// /editor/markdown.
func syncSkills(baseURL, spaceID, skillTypeID, folderID string) error {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("read skills dir %s: %w", skillsDir, err)
	}
	// Resolve the name property's id once — writes/filters key by propId, not xKey.
	skillPropID, err := skillNamePropID(baseURL, spaceID, skillTypeID)
	if err != nil {
		return fmt.Errorf("resolve skill name prop: %w", err)
	}
	if skillPropID == "" {
		return fmt.Errorf("skill type %s has no %q property", skillTypeID, skillNameXKey)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		skillName := strings.TrimSuffix(e.Name(), ".md")
		content, err := os.ReadFile(filepath.Join(skillsDir, e.Name()))
		if err != nil {
			return fmt.Errorf("read skill %s: %w", e.Name(), err)
		}

		objectID, err := findSkillObject(baseURL, spaceID, skillTypeID, skillPropID, skillName)
		if err != nil {
			return fmt.Errorf("query skill %s: %w", skillName, err)
		}

		if objectID != "" {
			if err := setObjectMarkdown(baseURL, spaceID, objectID, string(content)); err != nil {
				return fmt.Errorf("update skill %s: %w", skillName, err)
			}
			fmt.Fprintf(os.Stderr, "synced skill %s (updated %s)\n", skillName, objectID)
		} else {
			objectID, err = createSkillObject(baseURL, spaceID, skillTypeID, skillPropID, skillName, string(content))
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

// skillNamePropID returns the propId of the Agent Skill type's name property
// (xKey skillNameXKey), or "" if absent. Values are stored under the propId,
// so this is needed for every read/write/filter of the name.
func skillNamePropID(baseURL, spaceID, skillTypeID string) (string, error) {
	resp, err := http.Get(baseURL + "/v1/spaces/" + url.PathEscape(spaceID) + "/types/" + url.PathEscape(skillTypeID) + "/properties")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("list properties: %d %s", resp.StatusCode, msg)
	}
	var out struct {
		Properties []struct {
			Id   string `json:"id"`
			XKey string `json:"xKey"`
			Name string `json:"name"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	for _, p := range out.Properties {
		if p.XKey == skillNameXKey || p.Name == skillNameXKey {
			return p.Id, nil
		}
	}
	return "", nil
}

func findSkillObject(baseURL, spaceID, skillTypeID, skillPropID, skillName string) (string, error) {
	filter := map[string]any{
		"filter": map[string]any{
			skillTypeID + "." + skillPropID: skillName,
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

func createSkillObject(baseURL, spaceID, skillTypeID, skillPropID, skillName, markdown string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"types": []string{skillTypeID},
		"initialProperties": map[string]any{
			"any": map[string]any{
				"name": "Skill: " + skillName,
			},
			skillTypeID: map[string]any{
				skillPropID: skillName,
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

// findTypeByXKey resolves a type by its stable xKey — the same handle the JS
// side (anyHelper._resolveTypeSeg, getObjects, dotted property paths) keys on.
// Returns "" if no type carries that xKey. Type existence MUST be decided by
// xKey, not display name: a type created before the xKey feature (or by a
// different name) has no xKey, so a name match would reuse an xKey-less type
// that the agent can no longer resolve. Keying on xKey instead means the
// bootstrap creates a correct xKey-bearing type (the stale one orphans, ignored
// by xKey resolution), which self-heals a space carrying pre-xKey types.
func findTypeByXKey(baseURL, spaceID, xKey string) (string, error) {
	resp, err := http.Get(baseURL + "/v1/spaces/" + url.PathEscape(spaceID) + "/types")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Types []struct {
			Id   string `json:"id"`
			XKey string `json:"xKey"`
		} `json:"types"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	for _, t := range out.Types {
		if t.XKey == xKey {
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
// read from <toolDescriptionsDir>/<name>.md on disk. Returns "" if
// no description file exists for the given name.
func toolDescription(name string) string {
	data, err := os.ReadFile(filepath.Join(toolDescriptionsDir, name+".md"))
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
// anyHelper.js is read from anyHelperPath on disk and synced as
// anyHelper@v1 before the rest of the programs dir.
func syncPrograms(baseURL, spaceID, programTypeID, dir string, skipNames map[string]bool, folderID string) error {
	anyHelperJS, err := os.ReadFile(anyHelperPath)
	if err != nil {
		return fmt.Errorf("read anyHelper.js at %s: %w", anyHelperPath, err)
	}
	if err := upsertProgram(baseURL, spaceID, programTypeID, "anyHelper", "v1", string(anyHelperJS), folderID); err != nil {
		return fmt.Errorf("sync anyHelper: %w", err)
	}
	fmt.Fprintf(os.Stderr, "synced anyHelper@v1 from %s\n", anyHelperPath)

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

const (
	systemFolderName = "System Bobrik Files"
	// debugFolderName holds bobrik's agent-trace notes. It lives at the
	// nav ROOT — deliberately outside the system folder — so --bootstrap
	// (SIGHUP) refreshes never delete it and the accumulated traces
	// survive. (It used to be nested under the system folder; every
	// refresh deleted it and orphaned the traces parented inside.)
	debugFolderName = "Debug"
)

func ensureSystemFolder(baseURL, spaceID string) (string, error) {
	return ensureNavFolder(baseURL, spaceID, systemFolderName)
}

// ensureDebugFolder find-or-creates the root-level "Debug" nav folder
// that agent-trace notes are parented under. If the folder already
// exists it is reused untouched — never reparented, never recreated —
// so its id is stable and the traces inside survive every refresh.
func ensureDebugFolder(baseURL, spaceID string) (string, error) {
	return ensureNavFolder(baseURL, spaceID, debugFolderName)
}

// ensureNavFolder find-or-creates a top-level nav folder (nav.type=2)
// with the given name, returning its object id.
func ensureNavFolder(baseURL, spaceID, name string) (string, error) {
	id, err := findNavFolder(baseURL, spaceID, name)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}

	createBody, _ := json.Marshal(map[string]any{
		"nav":               map[string]any{"type": 2},
		"initialProperties": map[string]any{"any": map[string]any{"name": name}},
	})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects",
		"application/json",
		bytes.NewReader(createBody),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create folder %q: %d %s", name, resp.StatusCode, msg)
	}
	var obj struct {
		ObjectId string `json:"objectId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return "", err
	}
	return obj.ObjectId, nil
}

// removeSystemFiles deletes the "System Bobrik Files" folder and every
// object parented under it. Children are deleted before the folder so the
// nav tree doesn't have a moment with dangling parentIds. Missing folder
// is a no-op, not an error — refresh should still re-bootstrap cleanly.
func removeSystemFiles(baseURL, spaceID string) error {
	folderID, err := findSystemFolder(baseURL, spaceID)
	if err != nil {
		return fmt.Errorf("find system folder: %w", err)
	}
	if folderID == "" {
		return nil
	}

	childFilter := map[string]any{"filter": map[string]any{"nav.parentId": folderID}}
	body, _ := json.Marshal(childFilter)
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/query",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("query children: %w", err)
	}
	var out struct {
		Records []struct {
			Id string `json:"id"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		resp.Body.Close()
		return fmt.Errorf("decode children: %w", err)
	}
	resp.Body.Close()

	for _, rec := range out.Records {
		if err := deleteObject(baseURL, spaceID, rec.Id); err != nil {
			fmt.Fprintf(os.Stderr, "delete child %s: %v\n", rec.Id, err)
			continue
		}
	}
	fmt.Fprintf(os.Stderr, "removed %d children from %s\n", len(out.Records), folderID)

	if err := deleteObject(baseURL, spaceID, folderID); err != nil {
		return fmt.Errorf("delete folder %s: %w", folderID, err)
	}
	fmt.Fprintf(os.Stderr, "removed system folder %s\n", folderID)
	return nil
}

// findSystemFolder returns the System Bobrik Files folder id, or "" if
// no folder with that name + nav.type=2 exists.
func findSystemFolder(baseURL, spaceID string) (string, error) {
	return findNavFolder(baseURL, spaceID, systemFolderName)
}

// findNavFolder returns the id of the nav folder (nav.type=2) with the
// given name, or "" if none exists.
func findNavFolder(baseURL, spaceID, name string) (string, error) {
	filter := map[string]any{
		"filter": map[string]any{
			"any.name": name,
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
	if len(out.Records) == 0 {
		return "", nil
	}
	return out.Records[0].Id, nil
}

func deleteObject(baseURL, spaceID, objectID string) error {
	req, err := http.NewRequest(http.MethodDelete,
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/"+url.PathEscape(objectID),
		nil,
	)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
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
