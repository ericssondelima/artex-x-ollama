package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// textToolProvider recovers tool calls that a model wrote as text. Ollama and
// some OpenAI-compatible gateways return a finished message whose content is a
// JSON plan and whose tool_calls array is empty. The agent loop only executes
// native tool_use blocks, so that reply ends the turn. A native tool_use is
// forwarded unchanged. Text is recovered only when the name is one of the
// tools this request offered.
type textToolProvider struct {
	inner llm.Provider
}

func (p textToolProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	msg, stop, usage, err := p.inner.Complete(ctx, req)
	if err != nil {
		return msg, stop, usage, err
	}
	// A native tool_use whose name the model invented never reaches a handler:
	// the harness rejects it as an unknown tool. Rewrite it onto the real tool
	// before that happens. Text recovery below only runs when the model emitted
	// no tool call at all.
	if rewriteToolUses(&msg, toolCatalog(req)) && (stop == "" || stop == "end_turn") {
		stop = "tool_use"
	}
	if len(msg.ToolUses()) > 0 {
		return msg, stop, usage, nil
	}
	calls := parseTextToolCalls(msg.Text(), req.Tools)
	if len(calls) == 0 {
		return msg, stop, usage, err
	}
	logRecovered(calls)
	msg.Content = append(msg.Content, calls...)
	return msg, "tool_use", usage, nil
}

func (p textToolProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		var buffered []llm.StreamEvent
		var text strings.Builder
		sawTool := false
		for ev, err := range p.inner.Stream(ctx, req) {
			if err != nil {
				for _, b := range buffered {
					if !yield(b, nil) {
						return
					}
				}
				yield(ev, err)
				return
			}
			if ev.Type == llm.SEToolUseStart {
				sawTool = true
			}
			if ev.Type == llm.SETextDelta {
				text.WriteString(ev.Text)
			}
			buffered = append(buffered, ev)
		}
		if sawTool {
			buffered = rewriteToolEvents(buffered, toolCatalog(req))
			for _, b := range buffered {
				if !yield(b, nil) {
					return
				}
			}
			return
		}
		calls := parseTextToolCalls(text.String(), req.Tools)
		if len(calls) == 0 {
			for _, b := range buffered {
				if !yield(b, nil) {
					return
				}
			}
			return
		}
		logRecovered(calls)
		for _, b := range buffered {
			if b.Type == llm.SEMessageStop {
				continue
			}
			if !yield(b, nil) {
				return
			}
		}
		for i, c := range calls {
			if !yield(llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: c.ID, ToolName: c.Name, ToolIndex: i}, nil) {
				return
			}
			if !yield(llm.StreamEvent{Type: llm.SEToolInputJSON, Text: string(c.Input), ToolIndex: i}, nil) {
				return
			}
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"}, nil) {
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}

func allowedSet(tools []llm.ToolSchema) map[string]bool {
	allowed := map[string]bool{}
	for _, t := range tools {
		if t.Name != "" {
			allowed[t.Name] = true
		}
	}
	return allowed
}

func logRecovered(calls []llm.ContentBlock) {
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.Name
	}
	log.Printf("[llm] recovered %d tool call(s) from assistant text: %s", len(calls), strings.Join(names, ", "))
}

var (
	toolCallTag = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*</tool_call>`)
	codeFence   = regexp.MustCompile("(?s)```(?:json|tool_call|tool)?[ \\t]*\\n?(.*?)```")
)

// parseTextToolCalls turns assistant text into tool_use blocks whose names are
// in the tools offered on this request. Text that names nothing offered is left
// for the model loop to treat as a normal answer.
func parseTextToolCalls(text string, tools []llm.ToolSchema) []llm.ContentBlock {
	allowed := allowedSet(tools)
	if len(allowed) == 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	var calls []llm.ContentBlock
	for _, blob := range extractJSONBlobs(text) {
		var v any
		if json.Unmarshal([]byte(blob), &v) != nil {
			continue
		}
		calls = append(calls, interpretValue(v, allowed)...)
	}
	calls = dedupeCalls(calls)
	// gpt-oss often writes the intent as a markdown heading or a table and never
	// calls add_intent. That reply ends the planning round with no new work.
	if len(calls) == 0 && allowed["add_intent"] {
		if items := markdownIntents(text); len(items) > 0 {
			calls = []llm.ContentBlock{mustBlock("add_intent", map[string]any{"intents": items})}
		}
	}
	stamp := time.Now().UnixNano()
	for i := range calls {
		calls[i].Type = llm.BlockToolUse
		calls[i].ID = fmt.Sprintf("texttool_%d_%d", stamp, i+1)
		if len(calls[i].Input) == 0 || !json.Valid(calls[i].Input) {
			calls[i].Input = json.RawMessage(`{}`)
		}
	}
	return calls
}

func extractJSONBlobs(text string) []string {
	var blobs []string
	rest := toolCallTag.ReplaceAllStringFunc(text, func(m string) string {
		sub := toolCallTag.FindStringSubmatch(m)
		if len(sub) == 2 {
			if j := firstJSONValue(sub[1]); j != "" {
				blobs = append(blobs, j)
			}
		}
		return ""
	})
	rest = codeFence.ReplaceAllStringFunc(rest, func(m string) string {
		sub := codeFence.FindStringSubmatch(m)
		if len(sub) == 2 {
			if j := firstJSONValue(sub[1]); j != "" {
				blobs = append(blobs, j)
			}
		}
		return ""
	})
	if j := firstJSONValue(rest); j != "" {
		blobs = append(blobs, j)
	}
	return blobs
}

// firstJSONValue returns the raw JSON of the first object or array in s.
func firstJSONValue(s string) string {
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(s[start:]))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return ""
	}
	return string(v)
}

func interpretValue(v any, allowed map[string]bool) []llm.ContentBlock {
	switch val := v.(type) {
	case []any:
		return interpretArray(val, allowed)
	case map[string]any:
		return interpretObject(val, allowed)
	default:
		return nil
	}
}

func interpretObject(obj map[string]any, allowed map[string]bool) []llm.ContentBlock {
	if tc, ok := obj["tool_calls"]; ok {
		if calls := interpretValue(tc, allowed); len(calls) > 0 {
			return calls
		}
	}
	if call, ok := envelopeCall(obj, allowed); ok {
		return []llm.ContentBlock{call}
	}
	if calls := keyedToolObject(obj, allowed); len(calls) > 0 {
		return calls
	}
	if call, ok := bareArgumentsCall(obj, allowed); ok {
		return []llm.ContentBlock{call}
	}
	// A single invented plan object, not wrapped in an array:
	// {"id":"intent_1","type":"SEARCH_TOOLS","params":{"name":"mcp__…"}}
	if allowed["add_intent"] && looksLikeDirection(obj) {
		if items := planIntents([]any{obj}); len(items) > 0 {
			return []llm.ContentBlock{mustBlock("add_intent", map[string]any{"intents": items})}
		}
	}
	return nil
}

func interpretArray(arr []any, allowed map[string]bool) []llm.ContentBlock {
	if len(arr) == 0 {
		return nil
	}
	var direct []llm.ContentBlock
	matched := 0
	for _, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			continue
		}
		// Array elements use the explicit call shapes only. Bare argument
		// matching belongs to a lone object, where the tool is unambiguous.
		calls := directFromObject(obj, allowed)
		if len(calls) == 0 {
			continue
		}
		direct = append(direct, calls...)
		matched++
	}
	if matched == len(arr) && len(direct) > 0 {
		return direct
	}
	if allowed["set_goals"] && looksLikeGoals(arr) {
		return []llm.ContentBlock{mustBlock("set_goals", map[string]any{"goals": arr})}
	}
	if allowed["add_intent"] {
		if items := planIntents(arr); len(items) > 0 {
			return []llm.ContentBlock{mustBlock("add_intent", map[string]any{"intents": items})}
		}
	}
	return nil
}

func directFromObject(obj map[string]any, allowed map[string]bool) []llm.ContentBlock {
	if tc, ok := obj["tool_calls"]; ok {
		if calls := interpretValue(tc, allowed); len(calls) > 0 {
			return calls
		}
	}
	if call, ok := envelopeCall(obj, allowed); ok {
		return []llm.ContentBlock{call}
	}
	return keyedToolObject(obj, allowed)
}

func envelopeCall(obj map[string]any, allowed map[string]bool) (llm.ContentBlock, bool) {
	name, args, ok := envelopeParts(obj)
	if !ok {
		return llm.ContentBlock{}, false
	}
	if !allowed[name] {
		return llm.ContentBlock{}, false
	}
	input, ok := coerceArgs(name, args)
	if !ok {
		return llm.ContentBlock{}, false
	}
	return mustBlock(name, input), true
}

func envelopeParts(obj map[string]any) (string, any, bool) {
	if fn, ok := obj["function"].(map[string]any); ok {
		if name, _ := fn["name"].(string); name != "" {
			args := fn["arguments"]
			if args == nil {
				args = fn["parameters"]
			}
			return name, args, true
		}
	}
	name, _ := firstString(obj, "name", "tool")
	if name == "" {
		return "", nil, false
	}
	if _, ok := obj["arguments"]; ok {
		return name, obj["arguments"], true
	}
	if _, ok := obj["parameters"]; ok {
		return name, obj["parameters"], true
	}
	if _, ok := obj["input"]; ok {
		return name, obj["input"], true
	}
	if _, ok := obj["args"]; ok {
		return name, obj["args"], true
	}
	return "", nil, false
}

func keyedToolObject(obj map[string]any, allowed map[string]bool) []llm.ContentBlock {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if allowed[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	var out []llm.ContentBlock
	for _, name := range keys {
		out = append(out, callsForToolValue(name, obj[name])...)
	}
	return out
}

func callsForToolValue(name string, v any) []llm.ContentBlock {
	switch val := v.(type) {
	case map[string]any:
		return []llm.ContentBlock{mustBlock(name, val)}
	case []any:
		var out []llm.ContentBlock
		for _, el := range val {
			obj, ok := el.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, mustBlock(name, obj))
		}
		return out
	case string:
		if name == "Bash" {
			return []llm.ContentBlock{mustBlock(name, map[string]any{"command": val})}
		}
		var parsed any
		if json.Unmarshal([]byte(val), &parsed) == nil {
			return callsForToolValue(name, parsed)
		}
	}
	return nil
}

func bareArgumentsCall(obj map[string]any, allowed map[string]bool) (llm.ContentBlock, bool) {
	hits := map[string]bool{}
	if _, ok := obj["goals"]; ok && allowed["set_goals"] {
		hits["set_goals"] = true
	}
	if _, ok := obj["intents"]; ok && allowed["add_intent"] {
		hits["add_intent"] = true
	}
	if _, ok := obj["summary"].(string); ok && allowed["add_intent"] {
		hits["add_intent"] = true
	}
	if _, ok := obj["constraints"]; ok && allowed["set_constraints"] {
		hits["set_constraints"] = true
	}
	if _, ok := obj["entries"]; ok && allowed["add_task_scope"] {
		hits["add_task_scope"] = true
	}
	if _, ok := obj["kind"].(string); ok {
		if _, ok := obj["value"]; ok && allowed["add_task_scope"] {
			hits["add_task_scope"] = true
		}
	}
	if _, ok := obj["text"].(string); ok {
		if _, ok := obj["type"]; ok && allowed["set_constraints"] {
			hits["set_constraints"] = true
		}
	}
	// gpt-oss prints the deferred-tool lookup as the whole reply:
	// {"query":"select:mcp__tools__subfinder_scan"}
	// With no tool name, the worker loop treats that as the final answer.
	if q, ok := bareSearchQuery(obj); ok && allowed["SearchExtraTools"] {
		hits["SearchExtraTools"] = true
		obj["query"] = q
	}
	if len(hits) != 1 {
		return llm.ContentBlock{}, false
	}
	var name string
	for n := range hits {
		name = n
	}
	return mustBlock(name, obj), true
}

// bareSearchQuery matches a reply whose only field is the SearchExtraTools query.
func bareSearchQuery(obj map[string]any) (string, bool) {
	q, ok := obj["query"].(string)
	if !ok {
		return "", false
	}
	q = strings.TrimSpace(q)
	if q == "" || len(obj) != 1 {
		return "", false
	}
	if strings.HasPrefix(q, "select:") || strings.Contains(q, "mcp__") {
		return q, true
	}
	return "", false
}

func looksLikeGoals(arr []any) bool {
	if len(arr) == 0 {
		return false
	}
	for _, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			return false
		}
		text, _ := obj["text"].(string)
		if strings.TrimSpace(text) == "" {
			return false
		}
		if _, ok := obj["inputs"]; ok {
			return false
		}
		if _, ok := obj["arguments"]; ok {
			return false
		}
	}
	return true
}

func looksLikeDirection(obj map[string]any) bool {
	if _, ok := obj["summary"]; ok {
		return true
	}
	if s, ok := obj["tool_name"].(string); ok && strings.TrimSpace(s) != "" {
		return true
	}
	if _, ok := obj["schema"]; ok {
		if s, ok := obj["task"].(string); ok && strings.TrimSpace(s) != "" {
			return true
		}
	}
	if _, ok := obj["inputs"]; ok || obj["params"] != nil || obj["arguments"] != nil {
		return true
	}
	if id, ok := obj["id"].(string); ok && strings.Contains(strings.ToLower(id), "intent") {
		return true
	}
	typ, _ := obj["type"].(string)
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "search_tools", "searchextratools", "search_extra_tools", "bash":
		return true
	}
	return strings.Contains(typ, "__")
}

func planIntents(arr []any) []map[string]any {
	var items []map[string]any
	for _, el := range arr {
		obj, ok := el.(map[string]any)
		if !ok {
			continue
		}
		obj = unwrapToolSearch(obj)
		summary, _ := firstString(obj, "summary", "name", "description", "title", "task")
		tool, _ := firstString(obj, "type", "tool", "tool_name")
		if strings.TrimSpace(summary) == "" && tool == "" {
			continue
		}
		if strings.TrimSpace(summary) == "" {
			summary = "Use " + tool
		}
		if tool != "" && !strings.Contains(summary, tool) {
			summary += ". Use " + tool
		}
		var inputs any
		for _, key := range []string{"inputs", "arguments", "parameters", "input", "schema"} {
			if v, ok := obj[key]; ok {
				inputs = v
				break
			}
		}
		if inputs != nil {
			if raw, err := json.Marshal(inputs); err == nil {
				summary += " with " + string(raw)
			}
		}
		items = append(items, map[string]any{"summary": summary})
		if len(items) == 8 {
			break
		}
	}
	return items
}

// unwrapToolSearch turns a meta step such as
// {"type":"SEARCH_TOOLS","params":{"name":"mcp__tools__subfinder_scan"}}
// into a direction that names the real tool. The copy is shallow so the
// caller's object stays intact.
func unwrapToolSearch(obj map[string]any) map[string]any {
	typ, _ := obj["type"].(string)
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "search_tools", "searchextratools", "search_extra_tools":
	default:
		return obj
	}
	params, _ := obj["params"].(map[string]any)
	if params == nil {
		if alt, ok := obj["arguments"].(map[string]any); ok {
			params = alt
		}
	}
	var name string
	if params != nil {
		name, _ = params["name"].(string)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if q, ok := obj["query"].(string); ok {
			name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q), "select:"))
		}
	}
	if name == "" || !strings.Contains(name, "__") {
		return obj
	}
	out := make(map[string]any, len(obj)+1)
	for k, v := range obj {
		out[k] = v
	}
	out["type"] = name
	inputs := map[string]any{}
	for k, v := range params {
		if k == "name" {
			continue
		}
		inputs[k] = v
	}
	if len(inputs) > 0 {
		out["inputs"] = inputs
	}
	return out
}

func firstString(obj map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		s, ok := obj[k].(string)
		if ok && strings.TrimSpace(s) != "" {
			return s, true
		}
	}
	return "", false
}

func coerceArgs(name string, args any) (any, bool) {
	switch v := args.(type) {
	case nil:
		return map[string]any{}, true
	case map[string]any:
		return v, true
	case string:
		var parsed any
		if json.Unmarshal([]byte(v), &parsed) == nil {
			if _, ok := parsed.(map[string]any); ok {
				return parsed, true
			}
		}
		if name == "Bash" {
			return map[string]any{"command": v}, true
		}
		return nil, false
	default:
		return nil, false
	}
}

func mustBlock(name string, input any) llm.ContentBlock {
	raw, err := json.Marshal(input)
	if err != nil || !json.Valid(raw) {
		raw = []byte(`{}`)
	}
	return llm.ContentBlock{Type: llm.BlockToolUse, Name: name, Input: raw}
}

var (
	summaryLine   = regexp.MustCompile(`(?m)\*\*\s*Summary\s*\*\*\s*:?\s*(.+)$`)
	intentHeading = regexp.MustCompile(`(?m)\*\*\s*Intent\s*:\s*(.+?)\*\*`)
	quoteTrim     = strings.NewReplacer("“", "", "”", "", "\"", "", "**", "", "<br>", " ")
)

// markdownIntents pulls a direction out of a reply that described an intent in
// prose instead of calling add_intent. It accepts a "**Summary**: …" line or the
// Summary column of a markdown table. Prompt-echo from a truncated resume
// ("make sure the intent payload…") is not a direction.
func markdownIntents(text string) []map[string]any {
	var items []map[string]any
	for _, m := range summaryLine.FindAllStringSubmatch(text, 4) {
		s := cleanIntentSummary(m[1])
		if s == "" {
			continue
		}
		items = append(items, map[string]any{"summary": s})
	}
	if len(items) > 0 {
		return items
	}
	for _, m := range intentHeading.FindAllStringSubmatch(text, 4) {
		s := cleanIntentSummary(m[1])
		if s == "" {
			continue
		}
		items = append(items, map[string]any{"summary": s})
	}
	if len(items) > 0 {
		return items
	}
	if s := summaryTableCell(text); s != "" {
		return []map[string]any{{"summary": s}}
	}
	return nil
}

func summaryTableCell(text string) string {
	lines := strings.Split(text, "\n")
	col := -1
	header := -1
	for i, line := range lines {
		if !strings.Contains(line, "|") {
			continue
		}
		cells := splitTableCells(line)
		for j, c := range cells {
			if strings.EqualFold(strings.Trim(c, "* "), "summary") {
				col = j
				header = i
			}
		}
		if header == i {
			break
		}
	}
	if header < 0 || col < 0 {
		return ""
	}
	for _, line := range lines[header+1:] {
		if !strings.Contains(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cells := splitTableCells(line)
		if col >= len(cells) {
			continue
		}
		if s := cleanIntentSummary(cells[col]); s != "" {
			return s
		}
	}
	return ""
}

func splitTableCells(line string) []string {
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cells = append(cells, p)
	}
	return cells
}

func cleanIntentSummary(s string) string {
	s = quoteTrim.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) < 40 || looksLikePromptEcho(s) {
		return ""
	}
	return s
}

func looksLikePromptEcho(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "intent payload") || strings.Contains(l, "do not add extra keys")
}

func dedupeCalls(calls []llm.ContentBlock) []llm.ContentBlock {
	if len(calls) < 2 {
		return calls
	}
	seen := map[string]bool{}
	out := make([]llm.ContentBlock, 0, len(calls))
	for _, c := range calls {
		key := c.Name + "\n" + string(c.Input)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}
