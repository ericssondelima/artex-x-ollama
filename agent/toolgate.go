package agent

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// mcpToolName matches an MCP tool id written into a constraint.
var mcpToolName = regexp.MustCompile(`mcp__[A-Za-z0-9_]+`)

var bashWord = regexp.MustCompile(`(?i)\bBash\b`)

// toolGate is the execution ceiling for one task. An allow list is active only
// when an Allow constraint names at least one mcp__ tool. Those are the only
// MCP tools that may run, and Bash is refused with them. Deny lines that name
// a tool refuse it even when no allow list is set.
type toolGate struct {
	allow map[string]bool
	deny  map[string]bool
}

type toolGateKey struct{}

func WithToolGate(ctx context.Context, g toolGate) context.Context {
	return context.WithValue(ctx, toolGateKey{}, g)
}

func toolGateFromContext(ctx context.Context) toolGate {
	if ctx == nil {
		return toolGate{}
	}
	g, _ := ctx.Value(toolGateKey{}).(toolGate)
	return g
}

func toolGateFrom(ts *db.ExplorationStore) toolGate {
	if ts == nil {
		return toolGate{}
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return toolGate{}
	}
	g := toolGate{}
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		names := mcpToolName.FindAllString(text, -1)
		if c.Kind == "allow" {
			for _, n := range names {
				if g.allow == nil {
					g.allow = map[string]bool{}
				}
				g.allow[n] = true
			}
			continue
		}
		for _, n := range names {
			if g.deny == nil {
				g.deny = map[string]bool{}
			}
			g.deny[n] = true
		}
		if bashWord.MatchString(text) {
			if g.deny == nil {
				g.deny = map[string]bool{}
			}
			g.deny["Bash"] = true
		}
	}
	return g
}

// reject returns the reason a call must not run. Empty means the call proceeds.
func (g toolGate) reject(name string, in json.RawMessage) string {
	if len(g.allow) == 0 && len(g.deny) == 0 {
		return ""
	}
	target := strings.TrimSpace(name)
	if strings.EqualFold(target, "ExecuteExtraTool") {
		var args struct {
			ToolName string `json:"tool_name"`
		}
		if json.Unmarshal(in, &args) != nil || strings.TrimSpace(args.ToolName) == "" {
			return ""
		}
		target = strings.TrimSpace(args.ToolName)
	}
	if g.denied(target) {
		return "blocked by an operation constraint: " + target
	}
	if len(g.allow) == 0 {
		return ""
	}
	if strings.EqualFold(target, "Bash") || strings.HasPrefix(target, "mcp__") && !g.allow[target] {
		return "blocked by the task allow list: " + target
	}
	return ""
}

// rejectIntent stops the planner from queueing a direction outside the allow
// list, or from turning prove_goal into a worker job.
func (g toolGate) rejectIntent(summary string) string {
	s := strings.ToLower(strings.TrimSpace(summary))
	if s == "prove_goal" {
		return "prove_goal is a planner tool, not an intent"
	}
	if len(g.allow) == 0 {
		return ""
	}
	for _, name := range mcpToolName.FindAllString(summary, -1) {
		if !g.allow[name] {
			return "blocked by the task allow list: " + name
		}
	}
	return ""
}

func (g toolGate) denied(name string) bool {
	if g.deny[name] || g.deny[strings.ToLower(name)] {
		return true
	}
	return strings.EqualFold(name, "Bash") && g.deny["Bash"]
}
