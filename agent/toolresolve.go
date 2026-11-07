package agent

import (
	"encoding/json"
	"log"
	"regexp"
	"strings"

	"github.com/Autumn-27/norma/llm"
)

// toolCatalog is every tool name this request can actually run: the schemas
// offered to the model, plus the deferred MCP names listed in the system prompt.
// The harness rejects anything else as an unknown tool.
func toolCatalog(req llm.CompletionRequest) map[string]bool {
	known := map[string]bool{}
	for _, t := range req.Tools {
		if t.Name != "" {
			known[t.Name] = true
		}
	}
	for _, seg := range req.System {
		m := deferredNames.FindStringSubmatch(seg)
		if len(m) != 2 {
			continue
		}
		for _, line := range strings.Split(m[1], "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				known[line] = true
			}
		}
	}
	return known
}

var deferredNames = regexp.MustCompile(`(?s)<available-deferred-tools>\s*(.*?)\s*</available-deferred-tools>`)

// rewriteToolUses corrects tool calls on a finished assistant message.
// It reports whether any call was changed.
func rewriteToolUses(msg *llm.Message, known map[string]bool) bool {
	if msg == nil || len(known) == 0 {
		return false
	}
	changed := false
	for i := range msg.Content {
		b := &msg.Content[i]
		if b.Type != llm.BlockToolUse {
			continue
		}
		name, input, ok := resolveToolCall(b.Name, b.Input, known)
		if !ok {
			continue
		}
		log.Printf("[llm] rewrote tool call %q -> %q", b.Name, name)
		b.Name = name
		b.Input = input
		changed = true
	}
	return changed
}

// rewriteToolEvents applies resolveToolCall to a buffered model stream.
// Tool-input deltas for a rewritten call are replaced by one corrected JSON event.
func rewriteToolEvents(events []llm.StreamEvent, known map[string]bool) []llm.StreamEvent {
	if len(known) == 0 || len(events) == 0 {
		return events
	}
	type span struct {
		start int
		name  string
		raw   strings.Builder
	}
	var spans []span
	for i, ev := range events {
		switch ev.Type {
		case llm.SEToolUseStart:
			spans = append(spans, span{start: i, name: ev.ToolName})
		case llm.SEToolInputJSON:
			if n := len(spans); n > 0 {
				spans[n-1].raw.WriteString(ev.Text)
			}
		}
	}
	if len(spans) == 0 {
		return events
	}
	type resolved struct {
		name    string
		input   json.RawMessage
		changed bool
	}
	fixed := make([]resolved, len(spans))
	anyChanged := false
	for i, sp := range spans {
		name, input, ok := resolveToolCall(sp.name, json.RawMessage(sp.raw.String()), known)
		fixed[i] = resolved{name: name, input: input, changed: ok}
		if ok {
			anyChanged = true
			log.Printf("[llm] rewrote tool call %q -> %q", sp.name, name)
		}
	}
	if !anyChanged {
		return events
	}
	out := make([]llm.StreamEvent, 0, len(events)+len(spans))
	spanIdx := -1
	for i, ev := range events {
		if spanIdx+1 < len(spans) && spans[spanIdx+1].start == i {
			spanIdx++
			if fixed[spanIdx].changed {
				ev.ToolName = fixed[spanIdx].name
				out = append(out, ev)
				out = append(out, llm.StreamEvent{
					Type:      llm.SEToolInputJSON,
					Text:      string(fixed[spanIdx].input),
					ToolIndex: ev.ToolIndex,
				})
				continue
			}
		}
		if ev.Type == llm.SEToolInputJSON && spanIdx >= 0 && fixed[spanIdx].changed {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// resolveToolCall maps a model-invented tool call onto a name in known.
// The bool is false when the call should be left untouched.
func resolveToolCall(name string, input json.RawMessage, known map[string]bool) (string, json.RawMessage, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len(known) == 0 {
		return name, input, false
	}
	changed := false
	for i := 0; i < 3; i++ {
		next, nextIn, ok := resolveOnce(name, input, known)
		if !ok {
			break
		}
		name, input = next, nextIn
		changed = true
	}
	if known[name] && strings.EqualFold(name, "SearchExtraTools") {
		if repaired, ok := repairSearchQuery(input); ok {
			input = repaired
			changed = true
		}
	}
	if !changed {
		return name, input, false
	}
	if len(input) == 0 || !json.Valid(input) {
		input = json.RawMessage(`{}`)
	}
	return name, input, true
}

func resolveOnce(name string, input json.RawMessage, known map[string]bool) (string, json.RawMessage, bool) {
	low := strings.ToLower(name)
	switch {
	case isToolEnvelope(low):
		// agent.run / assistant / tool.run wrap the real call as
		// {"name":"…","arguments":{…}} or {"tool_name":"…","params":{…}}.
		if n, in, ok := unwrapToolEnvelope(input); ok {
			return n, in, true
		}
	case low == "exec" || low == "container.exec" || low == "shell":
		if known["Bash"] {
			if in, ok := toBashInput(input); ok {
				return "Bash", in, true
			}
		}
	case low == "browse" || low == "browser" || low == "open_url":
		if known["WebFetch"] {
			return "WebFetch", input, true
		}
	case low == "search_tool" || low == "search_tools" || low == "search_extra_tools" || low == "searchextratools":
		if known["SearchExtraTools"] {
			return "SearchExtraTools", input, true
		}
	case low == "repo_browser.create_file" || low == "create_file":
		if known["Write"] {
			return "Write", writeFileInput(input), true
		}
	}
	if known[name] {
		return name, input, false
	}
	if match, ok := matchToolName(name, known); ok && match != name {
		return match, input, true
	}
	return name, input, false
}

func isToolEnvelope(name string) bool {
	switch name {
	case "tool", "agent", "assistant", "function", "functions":
		return true
	}
	head, _, ok := strings.Cut(name, ".")
	if !ok {
		return false
	}
	switch head {
	case "tool", "agent", "assistant", "function", "functions":
		return true
	}
	return false
}

func unwrapToolEnvelope(input json.RawMessage) (string, json.RawMessage, bool) {
	var obj map[string]any
	if json.Unmarshal(input, &obj) != nil {
		return "", nil, false
	}
	name, _ := firstString(obj, "name", "tool", "tool_name", "tool-name")
	name = strings.TrimSpace(name)
	if name == "" || isToolEnvelope(strings.ToLower(name)) {
		return "", nil, false
	}
	var args any
	for _, k := range []string{"arguments", "parameters", "params", "input", "args"} {
		if v, ok := obj[k]; ok {
			args = v
			break
		}
	}
	raw, ok := coerceCallInput(name, args)
	if !ok {
		return "", nil, false
	}
	return name, raw, true
}

func coerceCallInput(name string, args any) (json.RawMessage, bool) {
	switch v := args.(type) {
	case nil:
		return json.RawMessage(`{}`), true
	case map[string]any:
		b, err := json.Marshal(v)
		return b, err == nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return json.RawMessage(`{}`), true
		}
		var parsed any
		if json.Unmarshal([]byte(s), &parsed) == nil {
			if _, ok := parsed.(map[string]any); ok {
				return json.RawMessage(s), true
			}
		}
		if name == "Bash" {
			return mustJSON(map[string]any{"command": s}), true
		}
		return mustJSON(map[string]any{"domain": s}), true
	default:
		return nil, false
	}
}

func toBashInput(input json.RawMessage) (json.RawMessage, bool) {
	var obj map[string]any
	if json.Unmarshal(input, &obj) != nil {
		return nil, false
	}
	if cmd, ok := obj["command"].(string); ok && strings.TrimSpace(cmd) != "" {
		return mustJSON(map[string]any{"command": cmd}), true
	}
	switch c := obj["cmd"].(type) {
	case string:
		if strings.TrimSpace(c) == "" {
			return nil, false
		}
		return mustJSON(map[string]any{"command": c}), true
	case []any:
		parts := make([]string, 0, len(c))
		for _, el := range c {
			s, ok := el.(string)
			if !ok {
				return nil, false
			}
			parts = append(parts, s)
		}
		if len(parts) == 0 {
			return nil, false
		}
		command := strings.Join(parts, " ")
		if len(parts) >= 3 && (parts[0] == "bash" || parts[0] == "sh") && (parts[1] == "-lc" || parts[1] == "-c") {
			command = parts[len(parts)-1]
		}
		return mustJSON(map[string]any{"command": command}), true
	default:
		return nil, false
	}
}

// repairSearchQuery fills the required "query" field when the model put the
// tool name in "name" instead (often prefixed "-1::/").
func repairSearchQuery(input json.RawMessage) (json.RawMessage, bool) {
	var obj map[string]any
	if json.Unmarshal(input, &obj) != nil {
		return nil, false
	}
	if q, _ := obj["query"].(string); strings.TrimSpace(q) != "" {
		return nil, false
	}
	raw, _ := firstString(obj, "name", "tool", "tool_name", "tool-name", "search_query", "q")
	raw = strings.TrimSpace(raw)
	if i := strings.LastIndex(raw, "/"); i >= 0 && strings.Contains(raw[:i], "::") {
		raw = raw[i+1:]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	if !strings.HasPrefix(strings.ToLower(raw), "select:") {
		raw = "select:" + raw
	}
	obj["query"] = raw
	return mustJSON(obj), true
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// matchToolName finds the one real tool a hallucinated MCP name was aiming at.
// Non-MCP names match only on a case-insensitive exact hit. An ambiguous pair
// such as nmap_scan and nmap_advanced_scan is left unresolved.
func matchToolName(name string, known map[string]bool) (string, bool) {
	if known[name] {
		return name, true
	}
	low := canonTool(name)
	exact := ""
	for k := range known {
		if canonTool(k) == low {
			if exact != "" && exact != k {
				return "", false
			}
			exact = k
		}
	}
	if exact != "" {
		return exact, true
	}
	server, local, ok := splitMCP(name)
	if !ok {
		return "", false
	}
	qTokens := distinctiveTokens(local)
	if len(qTokens) == 0 {
		return "", false
	}
	best, bestScore, second := "", 0, 0
	for k := range known {
		ks, kl, kok := splitMCP(k)
		if !kok || !strings.EqualFold(ks, server) {
			continue
		}
		score := scoreTokens(qTokens, distinctiveTokens(kl))
		if score > bestScore {
			second = bestScore
			bestScore = score
			best = k
		} else if score > second {
			second = score
		}
	}
	if bestScore >= 3 && bestScore-second >= 2 {
		return best, true
	}
	if bestScore >= 2 && second == 0 {
		return best, true
	}
	return "", false
}

// canonTool folds case and separators so search_extra_tools matches SearchExtraTools.
func canonTool(name string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(name))
}

// writeFileInput maps a create_file call onto Write's file_path/content schema.
func writeFileInput(input json.RawMessage) json.RawMessage {
	var obj map[string]any
	if json.Unmarshal(input, &obj) != nil {
		return input
	}
	if _, ok := obj["file_path"]; !ok {
		if path, ok := obj["path"].(string); ok && strings.TrimSpace(path) != "" {
			obj["file_path"] = path
		}
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return input
	}
	return b
}

func splitMCP(name string) (server, local string, ok bool) {
	rest, found := strings.CutPrefix(strings.ToLower(name), "mcp__")
	if !found {
		return "", "", false
	}
	i := strings.Index(rest, "__")
	if i <= 0 || i+2 >= len(rest) {
		return "", "", false
	}
	return rest[:i], rest[i+2:], true
}

var genericToolTokens = map[string]bool{
	"scan": true, "query": true, "discovery": true, "tool": true,
	"run": true, "ai": true, "test": true, "the": true,
}

var toolSynonyms = map[string]string{
	"archive": "wayback", "wayback": "wayback", "historical": "wayback",
	"technology": "technolog", "technologies": "technolog",
	"technolog": "technolog", "tech": "technolog",
	"fingerprint": "technolog", "wappalyzer": "technolog",
	"whatweb": "technolog",
	"dns":     "dns", "dnsenum": "dns",
	"subdomain": "subfinder", "subfinder": "subfinder",
	"url": "url", "urls": "url",
}

func distinctiveTokens(local string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(strings.ToLower(local), func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	}) {
		if tok == "" || genericToolTokens[tok] {
			continue
		}
		if syn, ok := toolSynonyms[tok]; ok {
			tok = syn
		}
		out = append(out, tok)
	}
	return out
}

func scoreTokens(query, candidate []string) int {
	if len(query) == 0 || len(candidate) == 0 {
		return 0
	}
	total := 0
	for _, q := range query {
		best := 0
		for _, c := range candidate {
			s := tokenScore(q, c)
			if s > best {
				best = s
			}
		}
		total += best
	}
	return total
}

func tokenScore(a, b string) int {
	if a == b {
		return 3
	}
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	if len(short) >= 4 && strings.Contains(long, short) {
		return 2
	}
	if len(short) >= 3 && strings.Contains(long, short) {
		return 1
	}
	return 0
}
