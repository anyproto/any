package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	agentrt "github.com/anyproto/anytype-agent-runtime/runtime"
)

var (
	base          string
	programsDir   string
	spaceName     string
	chatName      string
	agentName     string
	programTypeID string
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7001", "any server address (host:port)")
	flag.StringVar(&programsDir, "programs-dir", "cmd/bobrik-watch/programs", "directory with .js program files to sync")
	flag.StringVar(&spaceName, "space", "bobrik", "space name (created if missing)")
	flag.StringVar(&chatName, "chat", "bobrik", "chat object name (created if missing)")
	flag.StringVar(&agentName, "agent-name", "bobrik", "fromAgent tag on replies")
	flag.Parse()
	base = "http://" + *addr

	spaceID, err := ensureSpace(spaceName)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "space %q → %s\n", spaceName, spaceID)

	objectID, err := ensureChat(spaceID, chatName)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "chat %q → %s\n", chatName, objectID)

	programTypeID, err = ensureProgramType(base, spaceID)
	if err != nil {
		log.Fatalf("ensure program type: %v", err)
	}
	skillTypeID, err := ensureSkillType(base, spaceID)
	if err != nil {
		log.Fatalf("ensure skill type: %v", err)
	}
	_ = skillTypeID

	skip := map[string]bool{
		"anytypeHelper": true,
	}
	if err := syncPrograms(base, spaceID, programTypeID, programsDir, skip); err != nil {
		log.Fatalf("sync programs: %v", err)
	}
	fmt.Fprintf(os.Stderr, "programs synced from %s\n", programsDir)

	if err := syncSkills(base, spaceID, skillTypeID); err != nil {
		log.Fatalf("sync skills: %v", err)
	}
	fmt.Fprintf(os.Stderr, "skills synced\n")

	fmt.Fprintf(os.Stderr, "subscribing to chat_messages…\n")
	if err := subscribe(spaceID, objectID); err != nil {
		log.Fatal(err)
	}
}

func ensureSpace(name string) (string, error) {
	resp, err := http.Get(base + "/v1/spaces")
	if err != nil {
		return "", fmt.Errorf("list spaces: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Spaces []struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"spaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode spaces: %w", err)
	}
	for _, s := range out.Spaces {
		if s.Name == name {
			return s.Id, nil
		}
	}
	return createSpace(name)
}

func createSpace(name string) (string, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	resp, err := http.Post(base+"/v1/spaces", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create space: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create space: %d %s", resp.StatusCode, msg)
	}
	var sp struct {
		Id string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sp); err != nil {
		return "", fmt.Errorf("decode created space: %w", err)
	}
	fmt.Fprintf(os.Stderr, "created space %q → %s\n", name, sp.Id)
	return sp.Id, nil
}

func ensureChat(spaceID, name string) (string, error) {
	id, err := findObject(spaceID, name)
	if err == nil {
		return id, nil
	}
	return createChat(spaceID, name)
}

func findObject(spaceID, name string) (string, error) {
	filter := fmt.Sprintf(`{"filter":{"any.name":%q}}`, name)
	resp, err := http.Post(
		base+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/query",
		"application/json",
		strings.NewReader(filter),
	)
	if err != nil {
		return "", fmt.Errorf("query objects: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode objects: %w", err)
	}
	if len(out.Records) == 0 {
		return "", fmt.Errorf("object %q not found in space %s", name, spaceID)
	}
	var rec struct {
		Id string `json:"id"`
	}
	if err := json.Unmarshal(out.Records[0], &rec); err != nil {
		return "", fmt.Errorf("decode record: %w", err)
	}
	return rec.Id, nil
}

func createChat(spaceID, name string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"types": []string{"chat"},
		"initialProperties": map[string]any{
			"any": map[string]any{
				"name": name,
			},
		},
	})
	resp, err := http.Post(
		base+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("create chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create chat: %d %s", resp.StatusCode, msg)
	}
	var obj struct {
		ObjectId string `json:"objectId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return "", fmt.Errorf("decode created object: %w", err)
	}
	fmt.Fprintf(os.Stderr, "created chat %q → %s\n", name, obj.ObjectId)
	return obj.ObjectId, nil
}

func subscribe(spaceID, objectID string) error {
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/subscribe?dataset=chat_messages",
		url.PathEscape(spaceID), url.PathEscape(objectID))

	req, err := http.NewRequest("GET", base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("subscribe returned %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			eventType = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if eventType == "changes" && strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			handleChanges(spaceID, objectID, []byte(data))
		}
	}
	return scanner.Err()
}

func handleChanges(spaceID, objectID string, data []byte) {
	var events []struct {
		Records []struct {
			Id      string `json:"id"`
			Created bool   `json:"created"`
			Ops     []struct {
				Type    string          `json:"type"`
				Path    []string        `json:"path"`
				Payload json.RawMessage `json:"payload"`
			} `json:"ops"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &events); err != nil {
		return
	}
	for _, ev := range events {
		for _, rec := range ev.Records {
			if !rec.Created {
				continue
			}
			fields := extractFields(rec.Ops)
			if fields["fromAgent"] != "" {
				continue
			}
			text := fields["text"]
			creator := fields["creator"]
			fmt.Printf("new human message [%s] from %s: %s\n", rec.Id, creator, text)

			if err := runAgent(spaceID, objectID, text); err != nil {
				fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			}
		}
	}
}

func runAgent(spaceID, objectID, text string) error {
	fmt.Fprintf(os.Stderr, "starting agent runtime…\n")
	rt, err := agentrt.NewSobekRuntime()
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}

	SetupAnySDKDirtyRuntime(rt, AnySDKRuntimeConfig{
		APIBaseURL:     base,
		SpaceID:        spaceID,
		PrivateSpaceID: spaceID,
		ProgramTypeID:  programTypeID,
	})

	rt.SetEffectResolver("chatReply", func(tr *agentrt.TraceRecord, args ...any) any {
		if len(args) == 0 {
			return nil
		}
		msg := fmt.Sprintf("%v", args[0])
		tr.SetInput(msg)
		fmt.Fprintf(os.Stderr, "chatReply: %s\n", msg)
		if err := chatSend(spaceID, objectID, msg); err != nil {
			fmt.Fprintf(os.Stderr, "chatReply error: %v\n", err)
			return map[string]any{"error": err.Error()}
		}
		return nil
	})

	return runWrapperProgram(rt, spaceID, objectID, text)
}

func runWrapperProgram(rt agentrt.Runtime, spaceID, objectID, text string) error {
	quotedText, _ := json.Marshal(text)
	quotedSpaceID, _ := json.Marshal(spaceID)
	quotedChatID, _ := json.Marshal(objectID)
	quotedBaseURL, _ := json.Marshal(base)

	wrapper := fmt.Sprintf(`import { main as entryMain } from "private:init_agent@v1";
export function main() {
  return entryMain({
    text: %s, spaceId: %s, chatId: %s,
    apiBaseUrl: %s, verbose: false
  });
}`, string(quotedText), string(quotedSpaceID), string(quotedChatID), string(quotedBaseURL))

	fmt.Fprintf(os.Stderr, "evaluating wrapper…\n")
	result, err := rt.EvalToString("__wrapper__", wrapper, nil)
	if err != nil {
		return fmt.Errorf("eval wrapper: %w", err)
	}
	if result.Error != "" {
		return fmt.Errorf("js error: %s", result.Error)
	}
	fmt.Fprintf(os.Stderr, "agent result: %v\n", result.Result)
	if result.Trace != nil {
		traceJSON, _ := json.MarshalIndent(result.Trace, "", "  ")
		fmt.Fprintf(os.Stderr, "agent trace: %s\n", string(traceJSON))
	}
	return nil
}

func chatSend(spaceID, objectID, text string) error {
	body, _ := json.Marshal(map[string]string{
		"text":      text,
		"fromAgent": agentName,
	})
	resp, err := http.Post(
		base+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/"+url.PathEscape(objectID)+"/chat/messages",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chat send: %d %s", resp.StatusCode, msg)
	}
	return nil
}

func extractFields(ops []struct {
	Type    string          `json:"type"`
	Path    []string        `json:"path"`
	Payload json.RawMessage `json:"payload"`
}) map[string]string {
	out := map[string]string{}
	for _, op := range ops {
		if op.Type != "$set" {
			continue
		}
		if len(op.Path) == 0 {
			var bulk map[string]json.RawMessage
			if json.Unmarshal(op.Payload, &bulk) == nil {
				for k, v := range bulk {
					var s string
					if json.Unmarshal(v, &s) == nil {
						out[k] = s
					}
				}
			}
		} else if len(op.Path) == 1 {
			var s string
			if json.Unmarshal(op.Payload, &s) == nil {
				out[op.Path[0]] = s
			}
		}
	}
	return out
}
