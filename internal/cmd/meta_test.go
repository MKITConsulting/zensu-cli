package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetaScaffoldAgent_NoRESTEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("scaffold-agent must not call the REST API (adapter generation is MCP-server local)")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMetaCmd(f)
	err := runCmd(t, cmd, "scaffold-agent", "--cli", "all")
	if err == nil {
		t.Fatal("scaffold-agent should error: there is no REST endpoint for it")
	}
	if !strings.Contains(err.Error(), "scaffold-agent") {
		t.Errorf("error should explain scaffold-agent has no REST path, got: %v", err)
	}
}

func TestMetaSuggestWorkflow_NoRESTEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("suggest-workflow must not call the REST API (recommendation is composed in the MCP server)")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMetaCmd(f)
	err := runCmd(t, cmd, "suggest-workflow", "--product", "p1")
	if err == nil {
		t.Fatal("suggest-workflow should error: there is no single REST endpoint for it")
	}
	if !strings.Contains(err.Error(), "suggest-workflow") {
		t.Errorf("error should explain suggest-workflow has no REST path, got: %v", err)
	}
}

func TestMetaSuggestWorkflow_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMetaCmd(f)
	if err := runCmd(t, cmd, "suggest-workflow"); err == nil {
		t.Fatal("suggest-workflow without --product should error")
	}
}

func TestMetaWorkflowGuide_NoRESTEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("workflow-guide must not call the REST API (guide is served from a static MCP-server map)")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMetaCmd(f)
	err := runCmd(t, cmd, "workflow-guide", "bootstrap")
	if err == nil {
		t.Fatal("workflow-guide should error: there is no REST endpoint for it")
	}
	if !strings.Contains(err.Error(), "workflow-guide") {
		t.Errorf("error should explain workflow-guide has no REST path, got: %v", err)
	}
}

func TestMetaWorkflowGuide_RequiresArg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no workflow arg is given")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMetaCmd(f)
	if err := runCmd(t, cmd, "workflow-guide"); err == nil {
		t.Fatal("workflow-guide without a <workflow> arg should error")
	}
}

func TestMetaCommands_RejectRemovedJSONFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("MCP-only meta commands must not call the REST API")
	}))
	defer srv.Close()

	cases := [][]string{
		{"scaffold-agent", "--json"},
		{"suggest-workflow", "--json"},
		{"workflow-guide", "bootstrap", "--json"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f, _ := testFactory(srv)
			cmd := NewMetaCmd(f)
			err := runCmd(t, cmd, args...)
			if err == nil {
				t.Fatalf("%s should reject the removed --json flag", args[0])
			}
			if !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "--json") {
				t.Errorf("%s: expected unknown --json flag error, got: %v", args[0], err)
			}
		})
	}
}

func TestMetaCommands_HonestHelp(t *testing.T) {
	f, _ := testFactory(nil)
	meta := NewMetaCmd(f)

	mcpOnly := map[string]bool{
		"scaffold-agent":   true,
		"suggest-workflow": true,
		"workflow-guide":   true,
	}
	seen := 0
	for _, sub := range meta.Commands() {
		if !mcpOnly[sub.Name()] {
			continue
		}
		seen++
		t.Run(sub.Name(), func(t *testing.T) {
			if !strings.Contains(sub.Short, "(MCP-only)") {
				t.Errorf("Short should flag the command MCP-only, got: %q", sub.Short)
			}
			if !strings.Contains(sub.Long, "Not available over the REST CLI") {
				t.Errorf("Long should state the command is not available over REST, got: %q", sub.Long)
			}
			if !strings.Contains(sub.Long, "Zensu MCP server") {
				t.Errorf("Long should point to the Zensu MCP server, got: %q", sub.Long)
			}
		})
	}
	if seen != len(mcpOnly) {
		t.Errorf("expected to check %d MCP-only commands, saw %d", len(mcpOnly), seen)
	}
}
