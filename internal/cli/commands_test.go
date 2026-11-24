package cli

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestTypesAndCollectionsCommands pins the surface of the model: a
// collection group mirroring `any type`, and the object verbs for the
// one type and the many collections.
func TestTypesAndCollectionsCommands(t *testing.T) {
	root := newRootCmd()
	for _, path := range []string{
		"collection create",
		"collection list",
		"collection get",
		"collection update",
		"collection property list",
		"collection property add",
		"collection property patch",
		"collection property remove",
		"object create",
		"object type set",
		"object collection attach",
		"object collection detach",
	} {
		args := strings.Fields(path)
		cmd, rest, err := root.Find(args)
		if err != nil {
			t.Errorf("find %q: %v", path, err)
			continue
		}
		if len(rest) > 0 {
			t.Errorf("find %q: unmatched %v", path, rest)
			continue
		}
		if cmd.RunE == nil {
			t.Errorf("%q has no RunE", path)
		}
	}
}

// TestReadStateCommand pins the unread feed's command and flags.
func TestReadStateCommand(t *testing.T) {
	cmd, rest, err := newRootCmd().Find([]string{"read-state", "unread"})
	if err != nil || len(rest) > 0 || cmd.RunE == nil {
		t.Fatalf("read-state unread: cmd=%v rest=%v err=%v", cmd, rest, err)
	}
	for _, name := range []string{"cursor", "limit"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("read-state unread has no --%s", name)
		}
	}
}

// TestNoObjectTypeUnset — every object has exactly one type, so there
// is no verb that clears it.
func TestNoObjectTypeUnset(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"unset", "clear"} {
		cmd, rest, err := root.Find([]string{"object", "type", name})
		if err == nil && len(rest) == 0 {
			t.Errorf("object type %s still resolves to %s", name, cmd.CommandPath())
		}
	}
}

// TestObjectCreateFlags pins the create vocabulary: one --type, a
// repeatable --collection, one --from.
func TestObjectCreateFlags(t *testing.T) {
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"object", "create"})
	if err != nil {
		t.Fatalf("find object create: %v", err)
	}
	for flag, want := range map[string]string{"type": "string", "collection": "stringArray", "from": "string"} {
		f := cmd.Flags().Lookup(flag)
		if f == nil {
			t.Errorf("object create has no --%s", flag)
			continue
		}
		if got := f.Value.Type(); got != want {
			t.Errorf("--%s is %s, want %s", flag, got, want)
		}
	}
}

// TestNoWeightFlag — weight left the model; no command may still offer
// it.
func TestNoWeightFlag(t *testing.T) {
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Flags().Lookup("weight") != nil {
			t.Errorf("%s still has --weight", cmd.CommandPath())
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd())
}

// TestChildSpaceCommands pins the child-space commands and their flags.
func TestChildSpaceCommands(t *testing.T) {
	root := newRootCmd()
	for path, flags := range map[string][]string{
		"space create":     {"name", "description", "icon-cid", "type", "parent"},
		"space retry-push": nil,
		"space children":   nil,
		"child members":    nil,
		"child remove":     nil,
		"child ownership":  {"new-owner", "old-owner-perm"},
		"child delete":     {"yes"},
	} {
		cmd, rest, err := root.Find(strings.Fields(path))
		if err != nil || len(rest) > 0 || cmd.RunE == nil {
			t.Errorf("%s: cmd=%v rest=%v err=%v", path, cmd, rest, err)
			continue
		}
		for _, name := range flags {
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("%s has no --%s", path, name)
			}
		}
	}
}

// TestSpaceCreateFlags: `space create` posts --type as spaceType and
// --parent as parentSpaceId, and neither when the flag is not given.
func TestSpaceCreateFlags(t *testing.T) {
	addr, calls := cliServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"sp1","status":"active"}`)
	})
	for _, args := range [][]string{
		{"space", "create", "--name", "Team"},
		{"space", "create", "--name", "Acme", "--type", "any.organization"},
		{"space", "create", "--name", "Design", "--parent", "org1"},
		{"space", "create", "--parent", "org1", "--type", "any.organization.space"},
	} {
		if _, err := runCLI(t, addr, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	want := []cliCall{
		{http.MethodPost, "/v1/spaces", map[string]any{"name": "Team"}},
		{http.MethodPost, "/v1/spaces", map[string]any{"name": "Acme", "spaceType": "any.organization"}},
		{http.MethodPost, "/v1/spaces", map[string]any{"name": "Design", "parentSpaceId": "org1"}},
		{http.MethodPost, "/v1/spaces", map[string]any{"parentSpaceId": "org1", "spaceType": "any.organization.space"}},
	}
	if got := calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v\nwant %+v", got, want)
	}
}
