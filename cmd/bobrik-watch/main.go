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
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentrt "github.com/anyproto/anytype-agent-runtime/runtime"

	"github.com/anyproto/any/internal/anyrt"
)

var (
	base          string
	programsDir   string
	spaceName     string
	agentName     string
	programTypeID string

	// debugFolderID is the root-level "Debug" nav folder that
	// agent-trace notes are parented under. The folder is reused if it
	// exists (stable id), but a /bootstrap refresh (control-server
	// goroutine) still re-runs bootstrap and rewrites the variable while
	// the subscribe loop reads it — guard with debugFolderMu.
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
	flag.StringVar(&spaceName, "space", "bao", "space name (created if missing)")
	flag.StringVar(&agentName, "agent-name", "bao", "agent display name on replies (agent.name)")
	controlAddr := flag.String("control-addr", "127.0.0.1:7010", "bobrik control API address (host:port) — serves POST /bootstrap and /bootstrap-clean")
	bootstrap := flag.Bool("bootstrap", false, "POST /bootstrap to the running bobrik-watch (at --control-addr) for an incremental (hash-gated) refresh, and exit")
	bootstrapClean := flag.Bool("bootstrap-clean", false, "POST /bootstrap-clean to wipe \"System Bobrik Files\" and rebuild from scratch (recovery), and exit")
	flag.Parse()
	base = "http://" + *addr

	if *bootstrap || *bootstrapClean {
		endpoint := "/bootstrap"
		if *bootstrapClean {
			endpoint = "/bootstrap-clean"
		}
		if err := triggerBootstrap(*controlAddr, endpoint); err != nil {
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

	objectID, err := generalChat(spaceID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "general chat → %s\n", objectID)

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

	// Refresh is driven over HTTP (see control_server.go) instead of Unix
	// signals, so `--bootstrap` works on any platform and needs no PID file.
	go startControlServer(*controlAddr, spaceID, programTypeID, skillTypeID)

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

	// Orphan sweep: delete system-folder children whose source file is gone.
	// The hash-gated upserts above cover add/change; this covers delete, so a
	// plain restart fully reconciles disk → space without the destructive wipe
	// that --bootstrap-clean does.
	expected, err := expectedSystemNames(programsDir, skip)
	if err != nil {
		return "", fmt.Errorf("build expected names: %w", err)
	}
	if err := sweepOrphans(base, spaceID, sysFolderID, expected); err != nil {
		return "", fmt.Errorf("sweep orphans: %w", err)
	}
	fmt.Fprintf(os.Stderr, "orphan sweep complete\n")

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

// triggerBootstrap POSTs to a running bobrik-watch's control server (see
// control_server.go) to request a refresh — endpoint "/bootstrap" (incremental)
// or "/bootstrap-clean" (wipe-and-rebuild). Errors if nothing is listening or
// the server reports a non-2xx, so the caller `bobrik-watch --bootstrap` exits
// non-zero and scripts can detect "nothing was running." Replaces the old
// SIGHUP/SIGUSR1 + PID-file machinery so it works on any platform.
func triggerBootstrap(controlAddr, endpoint string) error {
	u := "http://" + controlAddr + endpoint
	resp, err := http.Post(u, "application/json", nil)
	if err != nil {
		return fmt.Errorf("POST %s (is bobrik-watch running?): %w", u, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s: %d %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	fmt.Fprintf(os.Stderr, "POST %s → %d %s\n", u, resp.StatusCode, strings.TrimSpace(string(body)))
	return nil
}

func ensureSpace(name string) (string, error) {
	// Resolve via GET /v1/spaces, adopting the first space named `name` whose
	// mapped Status is "active". The mapping (mapStatus in the SDK) collapses
	// raw (localStatus, remoteStatus) into one value and — as of SDK v0.0.9 —
	// honors BOTH delete sides: a locally-deleted-but-remotely-active space and
	// a locally-active-but-remotely-deleted one both map to "deleted", while an
	// owner-created space (localStatus stamped "active" by Create) maps to
	// "active". So "Status == active" is exactly "live and not deleted on
	// either side" — the condition we want, without consuming the raw
	// device-local fields directly. (Pre-v0.0.9 mapStatus was broken on both
	// counts, which is why this used to read the raw /v1/spaces/query rows; the
	// raw path re-minted a fresh space every restart because Create never
	// stamped localStatus, so it never matched its own prior space.)
	resp, err := http.Get(base + "/v1/spaces")
	if err != nil {
		return "", fmt.Errorf("list spaces: %w", err)
	}
	defer resp.Body.Close()
	// Fail loudly on a non-200 — otherwise a decode of the error body yields
	// zero spaces, which would silently mint a fresh space on every run.
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("list spaces: %d %s", resp.StatusCode, msg)
	}
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
		if s.Name == name && s.Status == "active" {
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

// generalChat resolves the space's single deterministic "general" chat
// object and returns its id. This is the derived chat (seed
// chat.GeneralChatSeed = "any/general-chat/v1") that every client — Desktop
// UI included — resolves for a space, surfaced as
// SpaceInfo.generalChatObjectId on the single-space GET /v1/spaces/:id
// response. That GET materializes it on first sight (derive → attach the
// chat type), so the id accepts chat/messages writes immediately and a
// joiner derives the same id the owner did.
//
// This replaced the earlier find-or-create-by-name scheme, which matched a
// plain object named "general" and minted one when absent — that could
// create duplicate "general" chats and, more importantly, wouldn't coincide
// with the space's real derived general chat that other clients show.
func generalChat(spaceID string) (string, error) {
	resp, err := http.Get(base + "/v1/spaces/" + url.PathEscape(spaceID))
	if err != nil {
		return "", fmt.Errorf("get space: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get space %s: %d %s", spaceID, resp.StatusCode, msg)
	}
	var sp struct {
		GeneralChatObjectId string `json:"generalChatObjectId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sp); err != nil {
		return "", fmt.Errorf("decode space: %w", err)
	}
	if sp.GeneralChatObjectId == "" {
		return "", fmt.Errorf("space %s returned no generalChatObjectId", spaceID)
	}
	return sp.GeneralChatObjectId, nil
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
				Text    string          `json:"text"`
				Creator string          `json:"creator"`
				Agent   json.RawMessage `json:"agent"`
			}
			if err := json.Unmarshal(rec.Doc, &doc); err != nil {
				fmt.Fprintf(os.Stderr, "decode chat doc %s: %v\n", rec.Id, err)
				continue
			}
			if len(doc.Agent) > 0 {
				continue
			}
			fmt.Printf("new human message [%s] from %s: %s\n", rec.Id, doc.Creator, doc.Text)

			if err := runAgent(spaceID, objectID, rec.Id, doc.Text); err != nil {
				fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
				// Terminal message so a client's typing indicator (keyed
				// on the last agent message's `done`) always resolves.
				// Best-effort — the error is already on stderr.
				if serr := chatSend(spaceID, objectID, "⚠ agent run failed: "+err.Error()); serr != nil {
					fmt.Fprintf(os.Stderr, "post agent-error message: %v\n", serr)
				}
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

	// No host-side `chatReply` effect anymore: chat replies go through
	// anyHelper.sendChatMessage in JS (one send path). The chat id reaches
	// the agent the same way it always has — as args.chatId, threaded from
	// runAgent's objectID through the wrapper into toolcall_core.
	anyrt.SetupAnySDKDirtyRuntime(rt, anyrt.RuntimeConfig{
		APIBaseURL:     base,
		SpaceID:        spaceID,
		PrivateSpaceID: spaceID,
		ProgramTypeID:  programTypeID,
		DebugFolderID:  getDebugFolderID(),
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

// chatSend posts a terminal, agent-authored message to the chat over the
// native chat API. It's the host-side fallback for the one case JS can't
// cover — the agent runtime failing to start — so a client's typing
// indicator (keyed on the last message's `done`) always resolves. The normal
// reply path is anyHelper.sendChatMessage from inside the agent; this Go
// poster exists only because there's no live runtime to post through here.
func chatSend(spaceID, objectID, text string) error {
	body := map[string]any{
		"text":  text,
		"agent": map[string]any{"name": agentName, "done": true},
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
