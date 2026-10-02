package agent

import (
	"os"
	"strings"
	"testing"
)

const lobster = "\U0001F99E"

// The model copies whatever decoration its prompt carries. The identity header
// and IDENTITY.md both used to include the lobster, so replies in Odoo chat
// ended with it. Neither may bring it back.
func TestIdentityPromptHasNoLobster(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	if strings.Contains(cb.getIdentity(), lobster) {
		t.Fatal("the built-in identity header must not contain the lobster emoji")
	}
}

func TestWorkspaceIdentityFilesHaveNoLobster(t *testing.T) {
	for _, path := range []string{
		"../../workspace/IDENTITY.md",
		"../../cmd/odooclaw/internal/onboard/workspace/IDENTITY.md",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if strings.Contains(string(data), lobster) {
			t.Fatalf("%s must not contain the lobster emoji", path)
		}
	}
}
