package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

func hexCatalog() map[string]bool {
	names := []string{
		"Bash", "Write", "SearchExtraTools",
		"mcp__tools__subfinder_scan",
		"mcp__tools__dnsenum_scan",
		"mcp__tools__detect_technologies_ai",
		"mcp__tools__waybackurls_discovery",
		"mcp__tools__nmap_scan",
		"mcp__tools__nmap_advanced_scan",
		"mcp__tools__httpx_probe",
	}
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	return known
}

func TestResolveToolCall_FixesObservedFailures(t *testing.T) {
	known := hexCatalog()
	cases := []struct {
		name    string
		input   string
		want    string
		contain string
	}{
		{"mcp__tools__dns_scan", `{"domain":"example.com"}`, "mcp__tools__dnsenum_scan", `"domain":"example.com"`},
		{"mcp__tools__technology_scan", `{"domain":"example.com"}`, "mcp__tools__detect_technologies_ai", `"domain"`},
		{"mcp__tools__archive_query", `{"query":"example.com"}`, "mcp__tools__waybackurls_discovery", `"query"`},
		{"container.exec", `{"cmd":["bash","-lc","echo test"]}`, "Bash", `"command":"echo test"`},
		{"exec", `{"cmd":["bash","-lc","env | grep TARGET"]}`, "Bash", `"command":"env | grep TARGET"`},
		{"tool", `{"name":"mcp__tools__subfinder_scan","arguments":"example.com"}`, "mcp__tools__subfinder_scan", `"domain":"example.com"`},
		{"tool.run", `{"name":"mcp__tools__subfinder_scan","arguments":{"target":"example.com"}}`, "mcp__tools__subfinder_scan", `"target":"example.com"`},
		{"SearchExtraTools", `{"name":"-1::/mcp__tools__subfinder_scan"}`, "SearchExtraTools", `"query":"select:mcp__tools__subfinder_scan"`},
		{"agent.run", `{"name":"SearchExtraTools","arguments":{"query":"mcp__tools__subfinder_scan"}}`, "SearchExtraTools", `"query":"mcp__tools__subfinder_scan"`},
		{"assistant", `{"tool_name":"mcp__tools__subfinder_scan","params":{"domain":"example.com"}}`, "mcp__tools__subfinder_scan", `"domain":"example.com"`},
		{"search_extra_tools", `{"search_query":"mcp__tools__nmap_scan"}`, "SearchExtraTools", `"query":"select:mcp__tools__nmap_scan"`},
		{"repo_browser.create_file", `{"path":"notes.txt","content":"hi"}`, "Write", `"file_path":"notes.txt"`},
	}
	for _, tc := range cases {
		gotName, gotIn, ok := resolveToolCall(tc.name, json.RawMessage(tc.input), known)
		if !ok || gotName != tc.want || !strings.Contains(string(gotIn), tc.contain) {
			t.Fatalf("%s -> ok=%v name=%s input=%s", tc.name, ok, gotName, gotIn)
		}
	}
	if _, _, ok := resolveToolCall("mcp__tools__nmap", json.RawMessage(`{"target":"example.com"}`), known); ok {
		t.Fatal("ambiguous nmap was rewritten")
	}
	if _, _, ok := resolveToolCall("Bash", json.RawMessage(`{"command":"true"}`), known); ok {
		t.Fatal("exact Bash was rewritten")
	}
	withFetch := hexCatalog()
	withFetch["WebFetch"] = true
	gotName, gotIn, ok := resolveToolCall("browse", json.RawMessage(`{"url":"https://example.com/"}`), withFetch)
	if !ok || gotName != "WebFetch" || !strings.Contains(string(gotIn), "https://example.com/") {
		t.Fatalf("browse -> ok=%v name=%s input=%s", ok, gotName, gotIn)
	}
}

func TestTextToolProvider_RewritesNativeToolName(t *testing.T) {
	p := textToolProvider{inner: stubProvider{msg: llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
		Type: llm.BlockToolUse, ID: "c1", Name: "mcp__tools__technology_scan", Input: json.RawMessage(`{"domain":"example.com"}`),
	}}}, stop: "tool_use"}}
	msg, stop, _, err := p.Complete(context.Background(), llm.CompletionRequest{
		Tools:  []llm.ToolSchema{{Name: "Bash"}},
		System: []string{"<available-deferred-tools>\nmcp__tools__detect_technologies_ai\nmcp__tools__dnsenum_scan\n</available-deferred-tools>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	uses := msg.ToolUses()
	if stop != "tool_use" || len(uses) != 1 || uses[0].Name != "mcp__tools__detect_technologies_ai" {
		t.Fatalf("stop=%s uses=%v", stop, uses)
	}
}

func TestRewriteToolEvents_ReplacesInputDeltas(t *testing.T) {
	events := []llm.StreamEvent{
		{Type: llm.SEToolUseStart, ToolID: "c1", ToolName: "container.exec", ToolIndex: 0},
		{Type: llm.SEToolInputJSON, Text: `{"cmd":["bash","-lc",`, ToolIndex: 0},
		{Type: llm.SEToolInputJSON, Text: `"echo test"]}`, ToolIndex: 0},
		{Type: llm.SEMessageStop},
	}
	got := rewriteToolEvents(events, hexCatalog())
	acc := llm.NewAccumulator()
	for _, ev := range got {
		acc.Add(ev)
	}
	uses := acc.Message().ToolUses()
	if len(uses) != 1 || uses[0].Name != "Bash" || !strings.Contains(string(uses[0].Input), `"command":"echo test"`) {
		t.Fatalf("uses=%v events=%v", uses, got)
	}
}

func TestNormalizeIntentPayload_TaskAndPriorityWord(t *testing.T) {
	in := []byte(`{"intents":[{"priority":"high","task":"Deduplicate discovered assets","tool":"mcp__tools__deduplicate_assets"}]}`)
	var out struct {
		Intents []struct {
			Summary  string `json:"summary"`
			Priority int    `json:"priority"`
		} `json:"intents"`
	}
	if err := json.Unmarshal(normalizeIntentPayload(in), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Intents) != 1 || out.Intents[0].Priority != 8 {
		t.Fatalf("out=%+v", out)
	}
	if !strings.Contains(out.Intents[0].Summary, "Deduplicate discovered assets") || !strings.Contains(out.Intents[0].Summary, "mcp__tools__deduplicate_assets") {
		t.Fatalf("summary=%q", out.Intents[0].Summary)
	}
}

func TestNoteLargeOutput(t *testing.T) {
	short := actool.Text("ok")
	if got := noteLargeOutput(short); got.Flatten() != "ok" {
		t.Fatalf("short changed: %q", got.Flatten())
	}
	long := actool.Text(strings.Repeat("a.example.com\n", 2000))
	got := noteLargeOutput(long).Flatten()
	if !strings.HasPrefix(got, "This tool output is data") || !strings.Contains(got, "a.example.com") {
		t.Fatalf("note missing: %s", got[:80])
	}
}
