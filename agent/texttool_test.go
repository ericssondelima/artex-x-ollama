package agent

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestParseTextToolCalls_KeyedGoalDecomposition(t *testing.T) {
	text := "```json\n" + `{
  "add_task_scope": [{"kind":"root_domain","value":"example.com","reason":"target"}],
  "set_constraints": [{"text":"passive only","type":"allow"}],
  "set_goals": [{"goals":[{"text":"Discover passive DNS","vulnclass":""}]}]
}` + "\n```"
	calls := parseTextToolCalls(text, []llm.ToolSchema{
		{Name: "add_task_scope"}, {Name: "set_constraints"}, {Name: "set_goals"},
	})
	if len(calls) != 3 {
		t.Fatalf("calls=%d want 3: %#v", len(calls), calls)
	}
	byName := map[string]llm.ContentBlock{}
	for _, c := range calls {
		byName[c.Name] = c
		if c.ID == "" || c.Type != llm.BlockToolUse {
			t.Fatalf("unstamped call: %+v", c)
		}
	}
	if !strings.Contains(string(byName["set_goals"].Input), "Discover passive DNS") {
		t.Fatalf("set_goals input: %s", byName["set_goals"].Input)
	}
	if !strings.Contains(string(byName["add_task_scope"].Input), "example.com") {
		t.Fatalf("scope input: %s", byName["add_task_scope"].Input)
	}
	if !strings.Contains(string(byName["set_constraints"].Input), "passive only") {
		t.Fatalf("constraint input: %s", byName["set_constraints"].Input)
	}
}

func TestParseTextToolCalls_PlannerPlanBecomesAddIntent(t *testing.T) {
	text := "```json\n" + `[
  {"id":"intent_001","type":"mcp__hexstrike__subfinder_scan","name":"Run subfinder for example.com","inputs":{"target":"example.com"}},
  {"id":"intent_002","type":"mcp__hexstrike__waybackurls_discovery","name":"Historical URLs","inputs":{"target":"example.com"}}
]` + "\n```"
	calls := parseTextToolCalls(text, []llm.ToolSchema{{Name: "add_intent"}, {Name: "Bash"}})
	if len(calls) != 1 || calls[0].Name != "add_intent" {
		t.Fatalf("got %#v", calls)
	}
	var in struct {
		Intents []struct {
			Summary string `json:"summary"`
		} `json:"intents"`
	}
	if err := json.Unmarshal(calls[0].Input, &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Intents) != 2 {
		t.Fatalf("intents=%d", len(in.Intents))
	}
	if !strings.Contains(in.Intents[0].Summary, "mcp__hexstrike__subfinder_scan") || !strings.Contains(in.Intents[0].Summary, "example.com") {
		t.Fatalf("summary: %s", in.Intents[0].Summary)
	}
}

func TestParseTextToolCalls_SearchToolsObjectBecomesAddIntent(t *testing.T) {
	text := `{"id":"intent_1","type":"SEARCH_TOOLS","params":{"name":"mcp__hexstrike__subfinder_scan"}}`
	calls := parseTextToolCalls(text, []llm.ToolSchema{{Name: "add_intent"}, {Name: "Bash"}})
	if len(calls) != 1 || calls[0].Name != "add_intent" {
		t.Fatalf("got %#v", calls)
	}
	if !strings.Contains(string(calls[0].Input), "mcp__hexstrike__subfinder_scan") {
		t.Fatalf("input: %s", calls[0].Input)
	}
}

func TestParseTextToolCalls_TagAndEnvelope(t *testing.T) {
	text := `<tool_call>{"name":"mcp__hexstrike__subfinder_scan","arguments":{"target":"example.com"}}</tool_call>`
	calls := parseTextToolCalls(text, []llm.ToolSchema{{Name: "mcp__hexstrike__subfinder_scan"}, {Name: "add_intent"}})
	if len(calls) != 1 || calls[0].Name != "mcp__hexstrike__subfinder_scan" {
		t.Fatalf("got %#v", calls)
	}
	if !strings.Contains(string(calls[0].Input), `"target":"example.com"`) {
		t.Fatalf("input: %s", calls[0].Input)
	}
}

func TestParseTextToolCalls_ToolNameObjectBecomesAddIntent(t *testing.T) {
	text := "```json\n" + `{
  "id": 62,
  "task": "Perform passive reconnaissance against example.com",
  "tool_name": "mcp__hexstrike__dns_scan",
  "schema": {"target": "example.com", "type": "dns_records"}
}` + "\n```"
	calls := parseTextToolCalls(text, []llm.ToolSchema{{Name: "add_intent"}, {Name: "Bash"}})
	if len(calls) != 1 || calls[0].Name != "add_intent" {
		t.Fatalf("got %#v", calls)
	}
	got := string(calls[0].Input)
	if !strings.Contains(got, "passive reconnaissance") || !strings.Contains(got, "mcp__hexstrike__dns_scan") || !strings.Contains(got, "example.com") {
		t.Fatalf("input: %s", got)
	}
}

func TestParseTextToolCalls_MarkdownIntentBecomesAddIntent(t *testing.T) {
	line := `**New Intent**` + "\n" + `- **Summary**: “Collect publicly available DNS records for the subdomains discovered by the previous subfinder scan.”`
	calls := parseTextToolCalls(line, []llm.ToolSchema{{Name: "add_intent"}})
	if len(calls) != 1 || calls[0].Name != "add_intent" {
		t.Fatalf("summary line: %#v", calls)
	}
	table := "**Proposed Intent**\n\n| IntentID | Parent(s) | State | Summary |\n|---|---|---|---|\n| 62 | 52 | queued | Aggregate the passive recon results, deduplicate the discovered assets, and record them as facts. |\n"
	calls = parseTextToolCalls(table, []llm.ToolSchema{{Name: "add_intent"}})
	if len(calls) != 1 || !strings.Contains(string(calls[0].Input), "deduplicate the discovered assets") {
		t.Fatalf("table: %#v", calls)
	}
	heading := "The goal is not met.\n\n**Intent: Produce the final passive reconnaissance summary as a fact.**\n"
	calls = parseTextToolCalls(heading, []llm.ToolSchema{{Name: "add_intent"}})
	if len(calls) != 1 || !strings.Contains(string(calls[0].Input), "final passive reconnaissance summary") {
		t.Fatalf("heading: %#v", calls)
	}
	echo := "- **Make sure the intent payload is lean** – only the fields that matter for the engine."
	if calls = parseTextToolCalls(echo, []llm.ToolSchema{{Name: "add_intent"}}); len(calls) != 0 {
		t.Fatalf("prompt echo recovered: %#v", calls)
	}
}

func TestParseTextToolCalls_SearchExtraToolsIntent(t *testing.T) {
	text := "```json\n{\"id\":82,\"type\":\"SearchExtraTools\",\"query\":\"select:mcp__hexstrike__subfinder_scan\",\"description\":\"Discover the schema for mcp__hexstrike__subfinder_scan so we can execute a passive scan of example.com.\"}\n```"
	calls := parseTextToolCalls(text, []llm.ToolSchema{{Name: "add_intent"}})
	if len(calls) != 1 || calls[0].Name != "add_intent" {
		t.Fatalf("intent: %#v", calls)
	}
	if !strings.Contains(string(calls[0].Input), "mcp__hexstrike__subfinder_scan") {
		t.Fatalf("tool name dropped: %s", calls[0].Input)
	}
}

func TestParseTextToolCalls_BareSearchQuery(t *testing.T) {
	text := `{"query":"select:mcp__hexstrike__subfinder_scan"}`
	calls := parseTextToolCalls(text, []llm.ToolSchema{{Name: "SearchExtraTools"}, {Name: "Bash"}})
	if len(calls) != 1 || calls[0].Name != "SearchExtraTools" {
		t.Fatalf("bare search: %#v", calls)
	}
	if !strings.Contains(string(calls[0].Input), "select:mcp__hexstrike__subfinder_scan") {
		t.Fatalf("query dropped: %s", calls[0].Input)
	}
	if calls = parseTextToolCalls(text, []llm.ToolSchema{{Name: "Bash"}}); len(calls) != 0 {
		t.Fatalf("search recovered without the tool: %#v", calls)
	}
	if calls = parseTextToolCalls(`{"query":"select:mcp__hexstrike__subfinder_scan","note":"later"}`, []llm.ToolSchema{{Name: "SearchExtraTools"}}); len(calls) != 0 {
		t.Fatalf("mixed object recovered: %#v", calls)
	}
}

func TestParseTextToolCalls_IgnoresUnknownAndProse(t *testing.T) {
	if calls := parseTextToolCalls("no json here", []llm.ToolSchema{{Name: "Bash"}}); len(calls) != 0 {
		t.Fatalf("prose recovered: %#v", calls)
	}
	text := `{"name":"NotATool","arguments":{"x":1}}`
	if calls := parseTextToolCalls(text, []llm.ToolSchema{{Name: "Bash"}}); len(calls) != 0 {
		t.Fatalf("unknown tool recovered: %#v", calls)
	}
	if calls := parseTextToolCalls(`{"name":"Bash","arguments":{"command":"dig example.com"}}`, nil); len(calls) != 0 {
		t.Fatalf("recovered with no offered tools: %#v", calls)
	}
}

func TestTextToolProvider_CompleteKeepsNativeCalls(t *testing.T) {
	native := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
		Type: llm.BlockToolUse, ID: "call_1", Name: "Bash", Input: json.RawMessage(`{"command":"true"}`),
	}}}
	p := textToolProvider{inner: stubProvider{msg: native, stop: "tool_use"}}
	msg, stop, _, err := p.Complete(context.Background(), llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "Bash"}}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != "tool_use" || len(msg.ToolUses()) != 1 || msg.ToolUses()[0].ID != "call_1" {
		t.Fatalf("native call rewritten: stop=%s uses=%v", stop, msg.ToolUses())
	}
}

func TestTextToolProvider_CompleteRecoversText(t *testing.T) {
	p := textToolProvider{inner: stubProvider{msg: llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		llm.TextBlock(`{"summary":"Map the public DNS for example.com"}`),
	}}, stop: "end_turn"}}
	msg, stop, _, err := p.Complete(context.Background(), llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "add_intent"}}})
	if err != nil {
		t.Fatal(err)
	}
	if stop != "tool_use" || len(msg.ToolUses()) != 1 || msg.ToolUses()[0].Name != "add_intent" {
		t.Fatalf("stop=%s uses=%v", stop, msg.ToolUses())
	}
}

func TestTextToolProvider_StreamInjectsRecoveredCall(t *testing.T) {
	inner := stubProvider{events: []llm.StreamEvent{
		{Type: llm.SETextDelta, Text: `{"name":"Bash","arguments":{"command":"dig example.com"}}`},
		{Type: llm.SEMessageDelta, StopReason: "end_turn"},
		{Type: llm.SEMessageStop},
	}}
	var got []llm.StreamEvent
	for ev, err := range (textToolProvider{inner: inner}).Stream(context.Background(), llm.CompletionRequest{Tools: []llm.ToolSchema{{Name: "Bash"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	acc := llm.NewAccumulator()
	for _, ev := range got {
		acc.Add(ev)
	}
	msg := acc.Message()
	if acc.StopReason != "tool_use" || len(msg.ToolUses()) != 1 || msg.ToolUses()[0].Name != "Bash" {
		t.Fatalf("stop=%s msg=%+v", acc.StopReason, msg)
	}
	if !strings.Contains(string(msg.ToolUses()[0].Input), "dig example.com") {
		t.Fatalf("input: %s", msg.ToolUses()[0].Input)
	}
}

type stubProvider struct {
	msg    llm.Message
	stop   string
	events []llm.StreamEvent
}

func (s stubProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return s.msg, s.stop, llm.Usage{}, nil
}

func (s stubProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, ev := range s.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}
