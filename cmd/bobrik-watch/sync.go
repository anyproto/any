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
func syncSkills(baseURL, spaceID, skillTypeID string) error {
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
			id, err := createSkillObject(baseURL, spaceID, skillTypeID, skillName, string(content))
			if err != nil {
				return fmt.Errorf("create skill %s: %w", skillName, err)
			}
			fmt.Fprintf(os.Stderr, "synced skill %s (created %s)\n", skillName, id)
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

// toolDescriptions maps program names to their tool descriptions.
// Programs with descriptions show up in getTools() and get injected
// as kernel globals in the agent boot prelude.
var toolDescriptions = map[string]string{
	"anyHelper": `Core API library for creating, reading, updating, and deleting objects, types, and programs.

## Tool Schema

### getObjects(typeKey, options?)
List objects of a type.
- typeKey: type key or name
- options.space: "user" (default) or "system"

### getObject(objId, opts?)
Fetch one object by ID. Returns properties + markdown body.
- objId: object ID
- opts.space: "user" or "system"
- opts.from, opts.to: line range for markdown slicing

### createObject(typeKey, data)
Create a new object.
- typeKey: type key or name
- data.name: display name
- data.body: markdown content
- data.properties: array of {key, text/number/checkbox} or object

### updateObject(objId, data)
Update an existing object.
- objId: object ID
- data.name, data.body/data.markdown, data.properties

### deleteObject(objId)
Delete an object.
- objId: object ID

### editObject(objId, opts)
Surgical string replacement on markdown body.
- objId: object ID
- opts.oldString, opts.newString, opts.replaceAll

### appendToObject(objId, text)
Append text to markdown body.
- objId: object ID
- text: text to append

### getTypes(opts?)
List all types in the space.

### createType(opts)
Create a type with properties.
- opts.key: type key
- opts.name: display name
- opts.properties: [{key, format}]

### describeType(typeKey)
Inspect a type: metadata, properties, sample object, object count.
- typeKey: type key or name

### getProperties()
List all properties across all types.

### search(queries...)
Search objects by text (limited — no FTS indexer yet).

### getSpaceMember(identityOrId)
Get a space member by identity or ID.

### listSpaceMembers()
List all space members.

### getCollectionObjects(collectionId)
List objects in a folder/collection.
- collectionId: folder object ID

### createCollection(name)
Create a folder.
- name: folder name

### addToCollection(collectionId, objectIds)
Move objects into a folder.
- collectionId: folder ID
- objectIds: single ID or array

### removeFromCollection(collectionId, objectId)
Move object back to root.`,

	"anyPrograms": `Program management — create, update, list, and inspect JS programs stored in the space.

## Tool Schema

### listPrograms()
List all programs in the space. Returns [{id, name, version, title}].

### getProgram(name, version?)
Get a program's source code. Returns {id, name, version, source}.
- name: program name
- version: version string (default "v1")

### createProgram(opts)
Create a new program.
- opts.name: program name (required)
- opts.source: JS source code (required)
- opts.version: version (default "v1")

### updateProgram(opts)
Update an existing program's source.
- opts.name: program name (required)
- opts.source: new source code (required)
- opts.version: version (default "v1")

### runProgram(name, args, version?)
Execute a program.
- name: program name
- args: arguments object
- version: version string (default "v1")

### editProgram(programName, opts)
Surgical string replacement on program source.
- programName: program name
- opts.oldString, opts.newString, opts.replaceAll`,

	"amemory": `Agent episodic memory — search, store, and manage persistent memories across conversations.

## Tool Schema

### createAMemory(client, opts)
Initialize the memory system. Returns a memory manager object with search/store methods.
- client: anyHelper client instance
- opts: configuration options`,

	"webSearch": `Web search — query the web for information.

## Tool Schema

### search(query, opts?)
Search the web. Returns [{title, url, text}].
- query: search query string
- opts: search options`,

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

	// Write tool description if this is a known tool
	if desc, ok := toolDescriptions[name]; ok {
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
