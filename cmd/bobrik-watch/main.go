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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentrt "github.com/anyproto/anytype-agent-runtime/runtime"
)

const pidFilePath = ".bobrik-pid"

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

	if _, err := bootstrapSystemFiles(spaceID, programTypeID, skillTypeID); err != nil {
		log.Fatalf("bootstrap system files: %v", err)
	}

	if err := writePIDFile(pidFilePath); err != nil {
		log.Fatalf("write pid file: %v", err)
	}
	defer os.Remove(pidFilePath)

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	go handleSignals(sigCh, spaceID, programTypeID, skillTypeID)

	fmt.Fprintf(os.Stderr, "subscribing to chat_messages…\n")
	subscribeLoop(spaceID, objectID)
}

// bootstrapSystemFiles (re)creates the "System Bobrik Files" folder and
// syncs all embedded programs + skills into it. Idempotent: existing
// programs/skills are updated rather than duplicated.
func bootstrapSystemFiles(spaceID, programTypeID, skillTypeID string) (string, error) {
	sysFolderID, err := ensureSystemFolder(base, spaceID)
	if err != nil {
		return "", fmt.Errorf("ensure system folder: %w", err)
	}
	fmt.Fprintf(os.Stderr, "system folder → %s\n", sysFolderID)

	skip := map[string]bool{"anyHelper": true}
	if err := syncPrograms(base, spaceID, programTypeID, programsDir, skip, sysFolderID); err != nil {
		return "", fmt.Errorf("sync programs: %w", err)
	}
	fmt.Fprintf(os.Stderr, "programs synced from %s\n", programsDir)

	if err := syncSkills(base, spaceID, skillTypeID, sysFolderID); err != nil {
		return "", fmt.Errorf("sync skills: %w", err)
	}
	fmt.Fprintf(os.Stderr, "skills synced\n")
	return sysFolderID, nil
}

func writePIDFile(path string) error {
	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(path, []byte(pid+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "pid file → %s (pid %s)\n", path, pid)
	return nil
}

func handleSignals(ch <-chan os.Signal, spaceID, programTypeID, skillTypeID string) {
	for sig := range ch {
		switch sig {
		case syscall.SIGHUP:
			fmt.Fprintf(os.Stderr, "SIGHUP received — refreshing System Bobrik Files\n")
			if err := removeSystemFiles(base, spaceID); err != nil {
				fmt.Fprintf(os.Stderr, "remove system files: %v\n", err)
			}
			if _, err := bootstrapSystemFiles(spaceID, programTypeID, skillTypeID); err != nil {
				fmt.Fprintf(os.Stderr, "rebootstrap: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "refresh complete\n")
			}
		case syscall.SIGINT, syscall.SIGTERM:
			fmt.Fprintf(os.Stderr, "%s received — exiting\n", sig)
			_ = os.Remove(pidFilePath)
			os.Exit(0)
		}
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
			Id     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"spaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode spaces: %w", err)
	}
	for _, s := range out.Spaces {
		if s.Name == name && s.Status != "deleted" {
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

func subscribeLoop(spaceID, objectID string) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		err := subscribe(spaceID, objectID)
		fmt.Fprintf(os.Stderr, "subscribe disconnected: %v — reconnecting in %s\n", err, backoff)
		time.Sleep(backoff)
		backoff = min(backoff*2, maxBackoff)
	}
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

	br := bufio.NewReader(resp.Body)
	var eventType string

	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) > 0 {
			line := strings.TrimRight(string(raw), "\r\n")

			if line == "" {
				eventType = ""
			} else if strings.HasPrefix(line, ":") {
				// comment / heartbeat
			} else if strings.HasPrefix(line, "event: ") {
				eventType = strings.TrimPrefix(line, "event: ")
			} else if eventType == "changes" && strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				handleChanges(spaceID, objectID, []byte(data))
			} else if eventType == "closed" {
				fmt.Fprintf(os.Stderr, "SSE closed frame: %s\n", line)
			}
		}
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("stream EOF (no closed frame)")
			}
			return err
		}
	}
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
		fmt.Fprintf(os.Stderr, "handleChanges: unmarshal error: %v (data: %.200s)\n", err, data)
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
