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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	agentrt "github.com/anyproto/anytype-agent-runtime/runtime"

	"github.com/anyproto/any/internal/anyrt"
)

const pidFilePath = ".bobrik-pid"

var (
	base          string
	programsDir   string
	spaceName     string
	agentName     string
	programTypeID string

	// debugFolderID is the root-level "Debug" nav folder that
	// agent-trace notes are parented under. The folder is reused if it
	// exists (stable id), but SIGHUP refresh (signal goroutine) still
	// re-runs bootstrap and rewrites the variable while the subscribe
	// loop reads it — guard with debugFolderMu.
	debugFolderMu sync.RWMutex
	debugFolderID string
)

func getDebugFolderID() string {
	debugFolderMu.RLock()
	defer debugFolderMu.RUnlock()
	return debugFolderID
}

func setDebugFolderID(id string) {
	debugFolderMu.Lock()
	defer debugFolderMu.Unlock()
	debugFolderID = id
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7001", "any server address (host:port)")
	flag.StringVar(&programsDir, "programs-dir", "cmd/bobrik-watch/programs", "directory with .js program files to sync")
	flag.StringVar(&spaceName, "space", "bobrik", "space name (created if missing)")
	flag.StringVar(&agentName, "agent-name", "bobrik", "fromAgent tag on replies")
	bootstrap := flag.Bool("bootstrap", false, "send SIGHUP to the running bobrik-watch (PID from "+pidFilePath+") and exit")
	flag.Parse()
	base = "http://" + *addr

	if *bootstrap {
		if err := triggerBootstrap(pidFilePath); err != nil {
			log.Fatal(err)
		}
		return
	}

	bobrikDir := filepath.Dir(programsDir)
	anyHelperPath = filepath.Join(bobrikDir, "anyHelper.js")
	skillsDir = filepath.Join(bobrikDir, "skills")
	toolDescriptionsDir = filepath.Join(bobrikDir, "tool-descriptions")

	spaceID, err := ensureSpace(spaceName)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "space %q → %s\n", spaceName, spaceID)

	objectID, err := ensureChat(spaceID)
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

	// Debug folder for agent-trace notes — root-level, outside the
	// system folder, so refresh never deletes it (traces accumulate).
	debugID, err := ensureDebugFolder(base, spaceID)
	if err != nil {
		return "", fmt.Errorf("ensure debug folder: %w", err)
	}
	setDebugFolderID(debugID)
	fmt.Fprintf(os.Stderr, "debug folder → %s\n", debugID)

	return sysFolderID, nil
}

// triggerBootstrap reads the PID file written by a running bobrik-watch
// and sends it SIGHUP, which the signal handler picks up as a refresh
// request. Errors if the PID file is missing or unparseable; the caller
// `bobrik-watch --bootstrap` then exits non-zero so scripts can detect
// "nothing was running."
func triggerBootstrap(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read pid file %s: %w", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("parse pid from %s: %w", path, err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("send SIGHUP to %d: %w", pid, err)
	}
	fmt.Fprintf(os.Stderr, "sent SIGHUP to %d\n", pid)
	return nil
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
	// Resolve via PR#29's windowed space-list query (raw tech-index rows),
	// NOT GET /v1/spaces. GET maps status through mapStatus(local, remote),
	// which reports a locally-deleted-but-remotely-active space as "active" —
	// so bobrik would adopt a space the user deleted (and the UI hides). The
	// raw rows expose localStatus directly; match on localStatus=="active",
	// the same field the UI filters on, so bobrik and the UI agree on which
	// "bobrik" space is live.
	body, _ := json.Marshal(map[string]any{"includeTotal": true})
	resp, err := http.Post(base+"/v1/spaces/query", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("query spaces: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Records []struct {
			Id          string `json:"id"`
			Name        string `json:"name"`
			LocalStatus string `json:"localStatus"`
		} `json:"records"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode spaces: %w", err)
	}
	for _, s := range out.Records {
		if s.Name == name && s.LocalStatus == "active" {
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

// chatName is the conventional name of the chat bobrik-watch watches. The
// agreed contract: clients (Desktop UI, etc.) create a plain chat object named
// "general" in each space, and bobrik finds-or-creates that one. This replaced
// the earlier deterministic-derive scheme (the any-ui/primary-chat/v1 seed) —
// the seed coupled bobrik to the UI's internal constant and produced a chat no
// client necessarily showed.
const chatName = "general"

// ensureChat find-or-creates the space's "general" chat object, returning its
// id. Find-or-create (not derive): it watches the chat a client already made,
// or mints one when bobrik owns the space (the dev "bobrik" space has no other
// client to create it). Matching on name + chat type means a re-run reuses the
// same chat instead of minting duplicates.
func ensureChat(spaceID string) (string, error) {
	id, err := findChat(spaceID)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}

	createBody, _ := json.Marshal(map[string]any{
		"types":             []string{"chat"},
		"initialProperties": map[string]any{"any": map[string]any{"name": chatName}},
	})
	resp, err := http.Post(
		base+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects",
		"application/json",
		bytes.NewReader(createBody),
	)
	if err != nil {
		return "", fmt.Errorf("create chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create chat %q: %d %s", chatName, resp.StatusCode, msg)
	}
	var obj struct {
		ObjectId string `json:"objectId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return "", fmt.Errorf("decode created chat: %w", err)
	}
	return obj.ObjectId, nil
}

// findChat returns the id of the chat object named "general", or "" if none
// exists. Constrains on the chat type (any.types contains "chat") so it never
// picks a non-chat object that happens to be named "general".
func findChat(spaceID string) (string, error) {
	filter := map[string]any{
		"filter": map[string]any{
			"any.name":  chatName,
			"any.types": "chat",
		},
	}
	body, _ := json.Marshal(filter)
	resp, err := http.Post(
		base+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/query",
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
	path := fmt.Sprintf("/v1/spaces/%s/query/subscribe", url.PathEscape(spaceID))
	body, _ := json.Marshal(map[string]any{
		"objectId": objectID,
		"dataset":  "chat_messages",
	})

	req, err := http.NewRequest("POST", base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Content-Type", "application/json")

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
			} else if eventType == "snapshot" {
				// Initial window — bobrik only reacts to messages
				// arriving live (Added events), so the snapshot is
				// just the "starting point" and we drop it.
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
	// Windowed query/subscribe wire shape: per batch, Added carries
	// newly-visible records with their full Doc inline. Updated and
	// Removed are ignored — bobrik only fires on fresh incoming
	// messages.
	var events []struct {
		Added []struct {
			Id  string          `json:"id"`
			Doc json.RawMessage `json:"doc"`
		} `json:"added"`
	}
	if err := json.Unmarshal(data, &events); err != nil {
		fmt.Fprintf(os.Stderr, "handleChanges: unmarshal error: %v (data: %.200s)\n", err, data)
		return
	}
	for _, ev := range events {
		for _, rec := range ev.Added {
			var doc struct {
				Text      string `json:"text"`
				Creator   string `json:"creator"`
				FromAgent string `json:"fromAgent"`
			}
			if err := json.Unmarshal(rec.Doc, &doc); err != nil {
				fmt.Fprintf(os.Stderr, "decode chat doc %s: %v\n", rec.Id, err)
				continue
			}
			if doc.FromAgent != "" {
				continue
			}
			fmt.Printf("new human message [%s] from %s: %s\n", rec.Id, doc.Creator, doc.Text)

			if err := runAgent(spaceID, objectID, rec.Id, doc.Text); err != nil {
				fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
			}
		}
	}
}

func runAgent(spaceID, objectID, msgID, text string) error {
	fmt.Fprintf(os.Stderr, "starting agent runtime…\n")
	rt, err := agentrt.NewSobekRuntime()
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}

	anyrt.SetupAnySDKDirtyRuntime(rt, anyrt.RuntimeConfig{
		APIBaseURL:     base,
		SpaceID:        spaceID,
		PrivateSpaceID: spaceID,
		ProgramTypeID:  programTypeID,
		DebugFolderID:  getDebugFolderID(),
	})

	rt.SetEffectResolver("chatReply", func(tr *agentrt.TraceRecord, args ...any) any {
		if len(args) == 0 {
			return nil
		}
		text, attachments := parseChatReplyArg(args[0])
		// Record what we actually sent to the server, not just the
		// stringified first arg — makes traces useful when the agent
		// is sending structured replies with attachments.
		traceInput := text
		if len(attachments) > 0 {
			b, _ := json.Marshal(map[string]any{"text": text, "attachments": attachments})
			traceInput = string(b)
		}
		tr.SetInput(traceInput)
		fmt.Fprintf(os.Stderr, "chatReply: %s (attachments=%d)\n", text, len(attachments))
		if err := chatSend(spaceID, objectID, text, attachments); err != nil {
			fmt.Fprintf(os.Stderr, "chatReply error: %v\n", err)
			return map[string]any{"error": err.Error()}
		}
		return nil
	})

	return runWrapperProgram(rt, spaceID, objectID, msgID, text)
}

func runWrapperProgram(rt agentrt.Runtime, spaceID, objectID, msgID, text string) error {
	quotedText, _ := json.Marshal(text)
	quotedSpaceID, _ := json.Marshal(spaceID)
	quotedChatID, _ := json.Marshal(objectID)
	quotedMsgID, _ := json.Marshal(msgID)
	quotedBaseURL, _ := json.Marshal(base)

	// msgId = the triggering chat_messages record id — lands on the turn
	// record's messageIds so agent_turns rows link back to the chat message.
	wrapper := fmt.Sprintf(`import { main as entryMain } from "private:init_agent@v1";
export function main() {
  return entryMain({
    text: %s, spaceId: %s, chatId: %s, msgId: %s,
    apiBaseUrl: %s, verbose: false
  });
}`, string(quotedText), string(quotedSpaceID), string(quotedChatID), string(quotedMsgID), string(quotedBaseURL))

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

// chatSend posts a chat message. `attachments` may be nil; entries
// must already be in the wire shape (map[id]{type,link}).
func chatSend(spaceID, objectID, text string, attachments map[string]any) error {
	body := map[string]any{
		"text":      text,
		"fromAgent": agentName,
	}
	if len(attachments) > 0 {
		body["attachments"] = attachments
	}
	raw, _ := json.Marshal(body)
	resp, err := http.Post(
		base+"/v1/spaces/"+url.PathEscape(spaceID)+"/objects/"+url.PathEscape(objectID)+"/chat/messages",
		"application/json",
		bytes.NewReader(raw),
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

// parseChatReplyArg normalizes the JS chatReply argument into the
// pieces chatSend needs.
//
// Accepted shapes:
//
//   - string (legacy)                        → {text: arg}
//   - {text, attachments?}                   → as-is, plus normalization
//   - anything else                          → fmt-stringified into text
//
// Attachments are accepted as either:
//
//   - map[id] -> {type, link}                — the wire shape
//   - map[id] -> "any://…" or "https://…"    — sugar: id of the form
//     `img_*` becomes type=image, everything else type=link. Lets the
//     agent write `{a1: "any://x"}` for the common case.
//
// Anything that doesn't normalize into the {type, link} shape is
// dropped silently — the server would reject it anyway, and the
// agent's main signal is "I got my text out" not "every key landed".
func parseChatReplyArg(arg any) (text string, attachments map[string]any) {
	switch v := arg.(type) {
	case string:
		return v, nil
	case map[string]any:
		if t, ok := v["text"].(string); ok {
			text = t
		} else {
			text = fmt.Sprintf("%v", v["text"])
		}
		if raw, ok := v["attachments"].(map[string]any); ok && len(raw) > 0 {
			attachments = normalizeAttachments(raw)
		}
		return text, attachments
	default:
		return fmt.Sprintf("%v", arg), nil
	}
}

func normalizeAttachments(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for id, raw := range in {
		switch entry := raw.(type) {
		case map[string]any:
			t, _ := entry["type"].(string)
			l, _ := entry["link"].(string)
			if t == "" || l == "" {
				continue
			}
			out[id] = map[string]any{"type": t, "link": l}
		case string:
			t := "link"
			if strings.HasPrefix(id, "img_") {
				t = "image"
			}
			out[id] = map[string]any{"type": t, "link": entry}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

