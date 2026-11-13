package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"gopkg.in/yaml.v3"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

func getAccountLimits(t *testing.T, e http.Handler) (api.AccountLimitsResponse, []byte) {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/account/limits", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.AccountLimitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return out, rec.Body.Bytes()
}

// editNodeconf returns raw with edit applied to the decoded nodeconf.
func editNodeconf(t *testing.T, raw []byte, edit func(conf map[string]any)) string {
	t.Helper()
	var conf map[string]any
	if err := yaml.Unmarshal(raw, &conf); err != nil {
		t.Fatal(err)
	}
	edit(conf)
	out, err := yaml.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// authorize boots an account on the server behind e — a new one, or
// accountId — and returns its id.
func authorize(t *testing.T, e http.Handler, accountId string) string {
	t.Helper()
	body := `{}`
	if accountId != "" {
		body = `{"accountId":"` + accountId + `"}`
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/auth", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	var resp api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.AccountId
}

// forgetStoredNodeconf drops the network configuration the coordinator
// last gave the account. The SDK prefers that copy over the configured
// one, so without this the next boot could still reach the nodes.
func forgetStoredNodeconf(t *testing.T, accountDir string) {
	t.Helper()
	err := filepath.WalkDir(accountDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || d.Name() != "nodeconf" {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return fs.SkipDir
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestServer_AccountLimits reads the limits from the network the tests
// boot on, then restarts the account with every node refusing the
// connection: the route serves the answer the network gave last.
func TestServer_AccountLimits(t *testing.T) {
	raw, err := os.ReadFile(stagingPath)
	if err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)
	accountId := authorize(t, e, "")

	// A file node whose own coordinator lookup failed has no pool to
	// report until it retries.
	var (
		live api.AccountLimitsResponse
		body []byte
	)
	for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(time.Second) {
		live, body = getAccountLimits(t, e)
		if live.Storage != nil && live.SharedSpaces != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the network never reported both parts: %s", body)
		}
	}
	for name, at := range map[string]time.Time{"storage": live.Storage.FetchedAt, "sharedSpaces": live.SharedSpaces.FetchedAt} {
		if age := time.Since(at); age < 0 || age > time.Minute {
			t.Errorf("%s.fetchedAt = %s, want a live answer", name, at)
		}
	}

	// The wire names, read without the response type.
	var wire map[string]map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	for part, fields := range map[string][]string{
		"storage":      {"limitBytes", "usageBytes", "fetchedAt"},
		"sharedSpaces": {"limit", "fetchedAt"},
	} {
		if len(wire[part]) != len(fields) {
			t.Errorf("%s = %v, want exactly %v", part, wire[part], fields)
		}
		for _, f := range fields {
			if _, ok := wire[part][f]; !ok {
				t.Errorf("%s.%s missing in %s", part, f, body)
			}
		}
	}

	// The same answer the SDK holds: a call this soon after is not a new
	// fetch, so the times match too.
	sdkLimits, err := d.sdk.Account().Limits(context.Background())
	if err != nil || sdkLimits.Storage == nil || sdkLimits.SharedSpaces == nil {
		t.Fatalf("sdk limits: %+v, %v", sdkLimits, err)
	}
	if live.Storage.LimitBytes != sdkLimits.Storage.LimitBytes || live.Storage.UsageBytes != sdkLimits.Storage.UsageBytes ||
		!live.Storage.FetchedAt.Equal(sdkLimits.Storage.FetchedAt) ||
		live.SharedSpaces.Limit != sdkLimits.SharedSpaces.Limit || !live.SharedSpaces.FetchedAt.Equal(sdkLimits.SharedSpaces.FetchedAt) {
		t.Errorf("route %+v / %+v, sdk %+v / %+v", live.Storage, live.SharedSpaces, sdkLimits.Storage, sdkLimits.SharedSpaces)
	}

	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)
	forgetStoredNodeconf(t, config.AccountDir(d.root, accountId))
	d.cfg.Network = config.Network{Nodeconf: editNodeconf(t, raw, func(conf map[string]any) {
		nodes, _ := conf["nodes"].([]any)
		for _, n := range nodes {
			n.(map[string]any)["addresses"] = []any{"127.0.0.1:1"}
		}
	})}
	authorize(t, e, accountId)

	start := time.Now()
	offline, body := getAccountLimits(t, e)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s: refused connections must not hold the reply", took)
	}
	if offline.Storage == nil || offline.SharedSpaces == nil {
		t.Fatalf("offline limits = %s, want the stored answer", body)
	}
	if *offline.Storage != *live.Storage || *offline.SharedSpaces != *live.SharedSpaces {
		t.Errorf("offline %+v / %+v, want the stored %+v / %+v", offline.Storage, offline.SharedSpaces, live.Storage, live.SharedSpaces)
	}
}

// TestServer_AccountLimits_NoNetwork: a device that never reached a
// network knows no limits, and that is a 200 with no parts. The reply is
// the same on a network with no file nodes, where storage is not asked
// for, and on one whose file nodes are out of reach, where it is asked
// and never answered.
func TestServer_AccountLimits_NoNetwork(t *testing.T) {
	withFileNode := editNodeconf(t, config.NodeconfPlaceholder(), func(conf map[string]any) {
		nodes, _ := conf["nodes"].([]any)
		conf["nodes"] = append(nodes, map[string]any{
			"peerId":    "FAKE_PEER_ID_FILEV2",
			"addresses": []any{"127.0.0.1:1"},
			"types":     []any{"fileV2"},
		})
	})
	for name, nodeconf := range map[string]string{
		"no file nodes":          string(config.NodeconfPlaceholder()),
		"unreachable file nodes": withFileNode,
	} {
		t.Run(name, func(t *testing.T) {
			d := newUnauthorizedDeps(t)
			d.cfg.Network = config.Network{Nodeconf: nodeconf}
			e := buildEcho(d)
			authorize(t, e, "")

			for range 2 {
				if _, body := getAccountLimits(t, e); string(body) != "{}\n" {
					t.Errorf("body = %q, want {}", body)
				}
			}
		})
	}
}
