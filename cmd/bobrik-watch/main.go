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
	jsDir         string
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
	flag.StringVar(&jsDir, "js-dir", "cmd/bobrik-watch/js", "root dir of bobrik JS assets (system/{js,md,skills} + integrations/{js,md})")
	flag.StringVar(&spaceName, "space", "bao", "space name (created if missing)")
	flag.StringVar(&agentName, "agent-name", "bao", "agent display name on replies (agent.name)")
	runAddr := flag.String("run-addr", "127.0.0.1:7010", "bobrik control API address — serves POST /run (run a deployed program in bobrik's kernel against a target space)")
	bootstrap := flag.Bool("bootstrap", false, "send SIGHUP to the running bobrik-watch (PID from "+pidFilePath+") for an incremental (hash-gated) refresh, and exit")
	bootstrapClean := flag.Bool("bootstrap-clean", false, "send SIGUSR1 to wipe \"System Bobrik Files\" and rebuild from scratch (recovery), and exit")
	flag.Parse()
	base = "http://" + *addr

	if *bootstrap || *bootstrapClean {
		sig := syscall.SIGHUP
		if *bootstrapClean {
			sig = syscall.SIGUSR1
		}
		if err := triggerBootstrap(pidFilePath, sig); err != nil {
			log.Fatal(err)
		}
		return
	}

	// Asset tree: <jsDir>/system/{js,md,skills} + <jsDir>/integrations/{js,md}.
	// Read live from disk at sync time (SIGHUP refresh re-reads them).
	systemProgramsDir = filepath.Join(jsDir, "system", "js")
	systemMdDir = filepath.Join(jsDir, "system", "md")
	skillsDir = filepath.Join(jsDir, "system", "skills")
	intProgramsDir = filepath.Join(jsDir, "integrations", "js")
	intMdDir = filepath.Join(jsDir, "integrations", "md")
	anyHelperPath = filepath.Join(systemProgramsDir, "anyHelper.js")

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
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGINT, syscall.SIGTERM)
	go handleSignals(sigCh, spaceID, programTypeID, skillTypeID)

	go startRunServer(*runAddr, spaceID)

	fmt.Fprintf(os.Stderr, "subscribing to chat_messages…\n")
	subscribeLoop(spaceID, objectID)
}

// bootstrapSystemFiles (re)creates the "System Bobrik Files" and "Integrations"
// nav folders and syncs the JS asset tree into them: system programs (+ skills)
// under the system folder, integration connectors under the Integrations
// folder. Idempotent: existing programs/skills are updated, not duplicated.
func bootstrapSystemFiles(spaceID, programTypeID, skillTypeID string) (string, error) {
	sysFolderID, err := ensureSystemFolder(base, spaceID)
	if err != nil {
		return "", fmt.Errorf("ensure system folder: %w", err)
	}
	fmt.Fprintf(os.Stderr, "system folder → %s\n", sysFolderID)

	intFolderID, err := ensureNavFolder(base, spaceID, integrationsFolderName)
	if err != nil {
		return "", fmt.Errorf("ensure integrations folder: %w", err)
	}
	fmt.Fprintf(os.Stderr, "integrations folder → %s\n", intFolderID)

	// anyHelper@v1 first (the client lib every other program imports), under
	// the system folder, with its tool doc from the system md dir.
	anyHelperJS, err := os.ReadFile(anyHelperPath)
	if err != nil {
		return "", fmt.Errorf("read anyHelper.js at %s: %w", anyHelperPath, err)
	}
	if err := upsertProgram(base, spaceID, programTypeID, "anyHelper", "v1", string(anyHelperJS), systemMdDir, sysFolderID); err != nil {
		return "", fmt.Errorf("sync anyHelper: %w", err)
	}

	skip := map[string]bool{"anyHelper": true}
	if err := syncPrograms(base, spaceID, programTypeID, systemProgramsDir, systemMdDir, skip, sysFolderID); err != nil {
		return "", fmt.Errorf("sync system programs: %w", err)
	}
	fmt.Fprintf(os.Stderr, "system programs synced from %s\n", systemProgramsDir)

	if err := syncSkills(base, spaceID, skillTypeID, sysFolderID); err != nil {
		return "", fmt.Errorf("sync skills: %w", err)
	}
	fmt.Fprintf(os.Stderr, "skills synced\n")

	if err := syncPrograms(base, spaceID, programTypeID, intProgramsDir, intMdDir, nil, intFolderID); err != nil {
		return "", fmt.Errorf("sync integration programs: %w", err)
	}
	fmt.Fprintf(os.Stderr, "integration programs synced from %s\n", intProgramsDir)

	// Orphan sweep per folder: delete a folder's children whose source file is
	// gone. The hash-gated upserts above cover add/change; this covers delete,
	// so a plain restart fully reconciles disk → space without the destructive
	// wipe that --bootstrap (SIGHUP) does.
	expectedSys, err := expectedSystemNames(systemProgramsDir, skip)
	if err != nil {
		return "", fmt.Errorf("build expected system names: %w", err)
	}
	if err := sweepOrphans(base, spaceID, sysFolderID, expectedSys); err != nil {
		return "", fmt.Errorf("sweep system orphans: %w", err)
	}
	expectedInt, err := expectedProgramNames(intProgramsDir, nil)
	if err != nil {
		return "", fmt.Errorf("build expected integration names: %w", err)
	}
	if err := sweepOrphans(base, spaceID, intFolderID, expectedInt); err != nil {
		return "", fmt.Errorf("sweep integration orphans: %w", err)
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

// triggerBootstrap reads the PID file written by a running bobrik-watch and
// sends it the given signal, which the signal handler picks up as a refresh
// request (SIGHUP = incremental, SIGUSR1 = wipe-and-rebuild). Errors if the
// PID file is missing or unparseable; the caller `bobrik-watch --bootstrap`
// then exits non-zero so scripts can detect "nothing was running."
func triggerBootstrap(path string, sig syscall.Signal) error {
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
	if err := proc.Signal(sig); err != nil {
		return fmt.Errorf("send %s to %d: %w", sig, pid, err)
	}
	fmt.Fprintf(os.Stderr, "sent %s to %d\n", sig, pid)
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
			// Incremental refresh — same hash-gated path as startup: unchanged
			// programs/skills are skipped, orphans swept. No wipe.
			fmt.Fprintf(os.Stderr, "SIGHUP received — refreshing System Bobrik Files (incremental)\n")
			if _, err := bootstrapSystemFiles(spaceID, programTypeID, skillTypeID); err != nil {
				fmt.Fprintf(os.Stderr, "rebootstrap: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "refresh complete\n")
			}
		case syscall.SIGUSR1:
			// Force-clean recovery — wipe the system folder + children, then
			// rebuild from scratch. For a divergent/corrupt space; the routine
			// path is the incremental SIGHUP above.
			fmt.Fprintf(os.Stderr, "SIGUSR1 received — wiping and rebuilding System Bobrik Files\n")
			if err := removeSystemFiles(base, spaceID); err != nil {
				fmt.Fprintf(os.Stderr, "remove system files: %v\n", err)
			}
			if _, err := bootstrapSystemFiles(spaceID, programTypeID, skillTypeID); err != nil {
				fmt.Fprintf(os.Stderr, "rebootstrap: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "clean rebuild complete\n")
			}
		case syscall.SIGINT, syscall.SIGTERM:
			fmt.Fprintf(os.Stderr, "%s received — exiting\n", sig)
			_ = os.Remove(pidFilePath)
			os.Exit(0)
		}
	}
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

// chatName is the conventional name of the chat bobrik-watch watches. The
// agreed contract: clients (Desktop UI, etc.) create a plain chat object named
// "general" in each space, and bobrik finds-or-creates that one. This replaced
// the earlier deterministic-derive scheme (the any-ui/primary-chat/v1 seed) —
// the seed coupled bobrik to the UI's internal constant and produced a chat no
// client necessarily showed.
const chatName = "general"

// ensureChat find-or-creates the space's "general" chat object, returning its
// id. Find-or-create (not derive): it watches the chat a client already made,
// or mints one when bobrik owns the space (the dev "bao" space has no other
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
