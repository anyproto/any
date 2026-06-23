package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anyproto/any/internal/anyrt"
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
		{"xKey": "any_tool", "name": "any_tool", "kind": "boolean"},
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
			// Skills round-trip through the markdown bridge (PUT parses to
			// blocks, GET re-renders), so the compare isn't byte-exact — but a
			// false "differs" only triggers a no-op PUT (markdown.Set diffs
			// blocks and writes only what changed), never spurious churn.
			if cur, gerr := getObjectMarkdown(baseURL, spaceID, objectID); gerr == nil &&
				strings.TrimSpace(cur) == strings.TrimSpace(string(content)) {
				fmt.Fprintf(os.Stderr, "unchanged skill %s (%s)\n", skillName, objectID)
				continue
			}
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

// getObjectMarkdown renders an object's blocks back to markdown via the
// editor bridge — the read side of setObjectMarkdown, used to compare a
// skill's stored content against disk before deciding to rewrite it.
func getObjectMarkdown(baseURL, spaceID, objectID string) (string, error) {
	resp, err := http.Get(baseURL + "/v1/spaces/" + url.PathEscape(spaceID) + "/objects/" + url.PathEscape(objectID) + "/editor/markdown")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get markdown: %d %s", resp.StatusCode, msg)
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Content, nil
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
	// Tool docs split: description body → program_description, per-method
	// records → program_methods, any_tool = "has description AND schema".
	description, methods := splitToolMarkdown(toolDescription(name))
	anyTool := description != "" && len(methods) > 0
	diskFP := programFingerprint(source, description, methods)

	objectID, err := anyrt.FindProgramObject(baseURL, spaceID, programTypeID, name, version)
	if err != nil {
		return fmt.Errorf("query %s@%s: %w", name, version, err)
	}

	if objectID != "" {
		// Hash gate: rebuild the fingerprint from the records already in the
		// space and skip every write when it equals the on-disk one. No stored
		// hash — the comparison is in-space content vs disk content directly,
		// so an unchanged boot produces zero new DAG changes (incl. nav, which
		// a prior boot already set). A read error falls through to a rewrite.
		if inFP, ferr := inSpaceProgramFingerprint(baseURL, spaceID, objectID); ferr == nil && inFP == diskFP {
			fmt.Fprintf(os.Stderr, "unchanged %s@%s (%s)\n", name, version, objectID)
			return nil
		}
		if err := modifyDataset(baseURL, spaceID, objectID, "program_source", "main", map[string]any{"code": source}); err != nil {
			return fmt.Errorf("update %s@%s: %w", name, version, err)
		}
		// Written explicitly both ways so a removed .md flips a stale true off.
		if err := setProgramProps(baseURL, spaceID, programTypeID, objectID, map[string]any{"any_tool": anyTool}); err != nil {
			return fmt.Errorf("set any_tool on %s@%s: %w", name, version, err)
		}
		fmt.Fprintf(os.Stderr, "synced %s@%s (updated %s)\n", name, version, objectID)
	} else {
		objectID, err = createProgramObject(baseURL, spaceID, programTypeID, name, version, source, anyTool)
		if err != nil {
			return fmt.Errorf("create %s@%s: %w", name, version, err)
		}
		fmt.Fprintf(os.Stderr, "synced %s@%s (created %s)\n", name, version, objectID)
	}

	if folderID != "" {
		_ = setNavParent(baseURL, spaceID, objectID, folderID)
	}

	if anyTool {
		if err := writeToolDocs(baseURL, spaceID, objectID, description, methods); err != nil {
			return fmt.Errorf("write tool docs for %s@%s: %w", name, version, err)
		}
	} else if err := clearToolDocs(baseURL, spaceID, objectID); err != nil {
		// A program that lost its .md must drop its stale doc records too,
		// or the fingerprint would mismatch on every subsequent boot.
		return fmt.Errorf("clear tool docs for %s@%s: %w", name, version, err)
	}
	return nil
}

// programFingerprint hashes the canonical (source, description, methods)
// triple that upsertProgram writes. The same fingerprint rebuilt from the
// in-space records (inSpaceProgramFingerprint) lets startup skip rewriting a
// program whose stored content is byte-identical to disk — no churn, no new
// DAG changes. Fields are length-prefixed so concatenation is unambiguous;
// methods are sorted by record id so read-back order can't flip the hash.
func programFingerprint(source, description string, methods []methodDoc) string {
	sorted := append([]methodDoc(nil), methods...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].BareName < sorted[j].BareName })

	var b strings.Builder
	fpField(&b, source)
	fpField(&b, description)
	for _, m := range sorted {
		fpField(&b, m.BareName)
		fpField(&b, m.Name)
		fpField(&b, m.Kind)
		fpField(&b, m.Text)
		fpField(&b, fmt.Sprintf("%d", m.Pos))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func fpField(b *strings.Builder, s string) {
	fmt.Fprintf(b, "%d:%s", len(s), s)
}

// inSpaceProgramFingerprint rebuilds programFingerprint from the records
// currently stored on the program object, so it can be compared to the
// on-disk fingerprint. Source/description/methods are stored verbatim (no
// markdown round-trip), so the comparison is exact. Any read error is
// surfaced so the caller falls back to an unconditional rewrite.
func inSpaceProgramFingerprint(baseURL, spaceID, objectID string) (string, error) {
	source, err := anyrt.QueryProgramSource(baseURL, spaceID, objectID)
	if err != nil {
		return "", err
	}
	description, err := readDatasetText(baseURL, spaceID, objectID, "program_description", "main", "text")
	if err != nil {
		return "", err
	}
	methods, err := readProgramMethods(baseURL, spaceID, objectID)
	if err != nil {
		return "", err
	}
	return programFingerprint(source, description, methods), nil
}

// clearToolDocs removes the description + every method record, so a program
// that lost its tool doc (.md deleted, any_tool now false) doesn't leave
// stale records that would forever mismatch the on-disk fingerprint. No-op
// when the datasets are already empty.
func clearToolDocs(baseURL, spaceID, objectID string) error {
	descIDs, err := datasetRecordIDs(baseURL, spaceID, objectID, "program_description")
	if err != nil {
		return err
	}
	if err := deleteRecords(baseURL, spaceID, objectID, "program_description", descIDs); err != nil {
		return err
	}
	methodIDs, err := datasetRecordIDs(baseURL, spaceID, objectID, "program_methods")
	if err != nil {
		return err
	}
	return deleteRecords(baseURL, spaceID, objectID, "program_methods", methodIDs)
}

// writeToolDocs writes the split tool docs: the description body to
// program_description/"main" and one program_methods record per method,
// then deletes stale method records the new doc no longer carries (a
// renamed/removed method would otherwise linger in listMethods forever).
func writeToolDocs(baseURL, spaceID, objectID, description string, methods []methodDoc) error {
	if err := modifyDataset(baseURL, spaceID, objectID, "program_description", "main", map[string]any{"text": description}); err != nil {
		return err
	}
	keep := make(map[string]bool, len(methods))
	for _, m := range methods {
		keep[m.BareName] = true
		if err := modifyDataset(baseURL, spaceID, objectID, "program_methods", m.BareName, map[string]any{
			"name": m.Name, "kind": m.Kind, "text": m.Text, "pos": m.Pos,
		}); err != nil {
			return err
		}
	}
	existing, err := datasetRecordIDs(baseURL, spaceID, objectID, "program_methods")
	if err != nil {
		return err
	}
	var stale []string
	for _, id := range existing {
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	return deleteRecords(baseURL, spaceID, objectID, "program_methods", stale)
}

// parseProgramFilename splits "name@version" → (name, version).
// If no @version, defaults to "v1".
func parseProgramFilename(baseName string) (name, version string) {
	if idx := strings.LastIndex(baseName, "@"); idx >= 0 {
		return baseName[:idx], baseName[idx+1:]
	}
	return baseName, "v1"
}

func createProgramObject(baseURL, spaceID, programTypeID, name, version, source string, anyTool bool) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"types": []string{programTypeID},
		"initialProperties": map[string]any{
			"any": map[string]any{
				"name": name + "@" + version,
			},
			programTypeID: map[string]any{
				"name":     name,
				"version":  version,
				"any_tool": anyTool,
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

// setProgramProps patches properties on an existing program object —
// same properties/:objId/set/:typeId endpoint setNavParent uses.
func setProgramProps(baseURL, spaceID, programTypeID, objectID string, patch map[string]any) error {
	body, _ := json.Marshal(map[string]any{"patch": patch})
	req, err := http.NewRequest(http.MethodPost,
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/properties/"+url.PathEscape(objectID)+"/set/"+url.PathEscape(programTypeID),
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
		return fmt.Errorf("set program props: %d %s", resp.StatusCode, msg)
	}
	return nil
}

// datasetRecordIDs lists the record ids currently in a dataset on an object.
func datasetRecordIDs(baseURL, spaceID, objectID, dataset string) ([]string, error) {
	body, _ := json.Marshal(map[string]any{"objectId": objectID, "dataset": dataset})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/query",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query %s: %d %s", dataset, resp.StatusCode, msg)
	}
	var out struct {
		Records []struct {
			Id string `json:"id"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Records))
	for _, r := range out.Records {
		ids = append(ids, r.Id)
	}
	return ids, nil
}

// queryDatasetRecords returns the raw records of a dataset on an object.
func queryDatasetRecords(baseURL, spaceID, objectID, dataset string) ([]json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"objectId": objectID, "dataset": dataset})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/query",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query %s: %d %s", dataset, resp.StatusCode, msg)
	}
	var out struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Records, nil
}

// readDatasetText reads a single string field from the named record of a
// dataset. A missing record/field reads as "" — matching the disk side,
// where an absent doc renders as the empty string.
func readDatasetText(baseURL, spaceID, objectID, dataset, recordID, field string) (string, error) {
	recs, err := queryDatasetRecords(baseURL, spaceID, objectID, dataset)
	if err != nil {
		return "", err
	}
	for _, r := range recs {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal(r, &rec); err != nil {
			return "", err
		}
		var id string
		if raw, ok := rec["id"]; ok {
			_ = json.Unmarshal(raw, &id)
		}
		if id != recordID {
			continue
		}
		if raw, ok := rec[field]; ok {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return "", err
			}
			return s, nil
		}
	}
	return "", nil
}

// readProgramMethods rebuilds the methodDoc slice from the program_methods
// dataset records (record id = BareName), the read-back counterpart of
// writeToolDocs.
func readProgramMethods(baseURL, spaceID, objectID string) ([]methodDoc, error) {
	recs, err := queryDatasetRecords(baseURL, spaceID, objectID, "program_methods")
	if err != nil {
		return nil, err
	}
	methods := make([]methodDoc, 0, len(recs))
	for _, r := range recs {
		var rec struct {
			Id   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
			Text string `json:"text"`
			Pos  int    `json:"pos"`
		}
		if err := json.Unmarshal(r, &rec); err != nil {
			return nil, err
		}
		methods = append(methods, methodDoc{
			BareName: rec.Id, Name: rec.Name, Kind: rec.Kind, Text: rec.Text, Pos: rec.Pos,
		})
	}
	return methods, nil
}

// deleteRecords tombstones dataset records. No-op on an empty id list.
func deleteRecords(baseURL, spaceID, objectID, dataset string, recordIDs []string) error {
	if len(recordIDs) == 0 {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"objectId": objectID, "dataset": dataset, "recordIds": recordIDs,
	})
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/delete-records",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete-records %s: %d %s", dataset, resp.StatusCode, msg)
	}
	return nil
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

// expectedSystemNames returns the set of object names (any.name) the current
// on-disk programs + skills should produce. sweepOrphans deletes any system-
// folder child not in this set — a program or skill whose source file was
// removed. Names mirror the create paths: programs "name@version"
// (createProgramObject), skills "Skill: name" (createSkillObject).
func expectedSystemNames(programsDir string, skip map[string]bool) (map[string]bool, error) {
	names := map[string]bool{"anyHelper@v1": true}

	progs, err := os.ReadDir(programsDir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", programsDir, err)
	}
	for _, e := range progs {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".js")
		if skip[base] {
			continue
		}
		name, version := parseProgramFilename(base)
		names[name+"@"+version] = true
	}

	skills, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir %s: %w", skillsDir, err)
	}
	for _, e := range skills {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names["Skill: "+strings.TrimSuffix(e.Name(), ".md")] = true
	}
	return names, nil
}

// sweepOrphans deletes objects parented under the system folder whose
// any.name is not in the expected set — i.e. a program or skill whose source
// file was removed from disk. The startup hash-gate handles add/change; this
// closes the loop on delete without the wipe-and-recreate that --bootstrap
// does. Children with an empty name are left untouched (defensive — the
// system folder is bobrik-exclusive, but never delete something unnamed).
func sweepOrphans(baseURL, spaceID, folderID string, expected map[string]bool) error {
	filter := map[string]any{"filter": map[string]any{"nav.parentId": folderID}}
	body, _ := json.Marshal(filter)
	resp, err := http.Post(
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/query",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("query children: %d %s", resp.StatusCode, msg)
	}
	var out struct {
		Records []struct {
			Id  string `json:"id"`
			Any struct {
				Name string `json:"name"`
			} `json:"any"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	for _, rec := range out.Records {
		if rec.Any.Name == "" || expected[rec.Any.Name] {
			continue
		}
		if err := deleteObject(baseURL, spaceID, rec.Id); err != nil {
			fmt.Fprintf(os.Stderr, "sweep orphan %s (%q): %v\n", rec.Id, rec.Any.Name, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "swept orphan %s (%q)\n", rec.Id, rec.Any.Name)
	}
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
		baseURL+"/v1/spaces/"+url.PathEscape(spaceID)+"/properties/"+url.PathEscape(objectID)+"/set/nav",
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
