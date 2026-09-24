package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A numbered list keeps its numbers and continuation lines across
// GET → PUT cycles, and re-PUTting a GET writes nothing.
func TestServer_MarkdownListNumbers(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	spaceID, objectID := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceID + "/objects/" + objectID
	md := base + "/editor/editor_blocks/markdown"

	for _, content := range []string{
		"10. Restart the computer.\n    Wait until the login screen appears.",
		"9. Parent\n   continued\n\n10. Next\n    continued\n    - Child",
		"1. a\n\n2. b\n\n3. c",
	} {
		t.Run(strings.SplitN(content, "\n", 2)[0], func(t *testing.T) {
			markdownSet(t, e, md, content)
			before := blocksList(t, e, base)
			for cycle := 0; cycle < 2; cycle++ {
				got := getMarkdown(t, e, md)
				require.Equal(t, content, got)
				result := markdownSet(t, e, md, got)
				require.Equal(t, len(before.Records), result.Unchanged)
				require.Empty(t, result.Inserted)
				require.Empty(t, result.Updated)
				require.Empty(t, result.Deleted)
			}
		})
	}
}
