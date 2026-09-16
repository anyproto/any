package cli

import (
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
// repeatable --collection.
func TestObjectCreateFlags(t *testing.T) {
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"object", "create"})
	if err != nil {
		t.Fatalf("find object create: %v", err)
	}
	for flag, want := range map[string]string{"type": "string", "collection": "stringArray"} {
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
