package connectors_test

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/connectors/claude"
	"github.com/klyne-ai/klyne/internal/connectors/codex"
)

// Compile-time assertions that both connectors satisfy the W0-frozen interface.
var (
	_ connectors.Connector = (*claude.Connector)(nil)
	_ connectors.Connector = (*codex.Connector)(nil)
)

func TestConnectorsSatisfyInterface(t *testing.T) {
	t.Log("claude.Connector and codex.Connector satisfy connectors.Connector at compile time")
}
