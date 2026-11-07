package agent

import (
	"regexp"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// ConstraintLine is one Allowed or Forbidden sentence copied from the task text.
type ConstraintLine struct {
	Kind string // allow | deny
	Text string
}

var constraintLabel = regexp.MustCompile(`(?i)(?:^|\n)\s*(Allowed|Forbidden)\s*:\s*`)

// ExplicitConstraintLines reads Allowed and Forbidden sentences from the
// description and the goal. Creation stores them when the decomposer ends
// without calling set_constraints, which is what arms the tool gate.
func ExplicitConstraintLines(description, goal string) []ConstraintLine {
	text := strings.TrimSpace(description)
	if g := strings.TrimSpace(goal); g != "" {
		if text != "" {
			text += "\n"
		}
		text += g
	}
	indexes := constraintLabel.FindAllStringSubmatchIndex(text, -1)
	if len(indexes) == 0 {
		return nil
	}
	var out []ConstraintLine
	for i, idx := range indexes {
		kind := "deny"
		if strings.EqualFold(text[idx[2]:idx[3]], "Allowed") {
			kind = "allow"
		}
		end := len(text)
		if i+1 < len(indexes) {
			end = indexes[i+1][0]
		}
		body := strings.TrimSpace(text[idx[1]:end])
		if body == "" {
			continue
		}
		out = append(out, ConstraintLine{Kind: kind, Text: body})
	}
	return out
}

// MissingConstraintLines returns the description lines that are not already
// stored. An Allow line that names mcp tools is missing until an allow row
// names those same tools, even when the model already saved the Deny lines.
func MissingConstraintLines(existing []db.Constraint, lines []ConstraintLine) []ConstraintLine {
	var out []ConstraintLine
	for _, line := range lines {
		if constraintLineCovered(existing, line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func constraintLineCovered(existing []db.Constraint, line ConstraintLine) bool {
	var allowBlob string
	for _, row := range existing {
		if row.Kind != line.Kind {
			continue
		}
		if strings.TrimSpace(row.Text) == line.Text || strings.Contains(row.Text, line.Text) {
			return true
		}
		if row.Kind == "allow" {
			allowBlob += " " + row.Text
		}
	}
	names := mcpToolName.FindAllString(line.Text, -1)
	if line.Kind != "allow" || len(names) == 0 || allowBlob == "" {
		return false
	}
	for _, name := range names {
		if !strings.Contains(allowBlob, name) {
			return false
		}
	}
	return true
}

// constraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block appended to the planner/worker system prompt. allow/deny are
// grouped; empty string when there are no constraints (or ts is nil). The framing
// deliberately puts these ABOVE the exploration/expansion heuristics so a declared
// boundary wins the tug-of-war against "chase another entry surface".
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var allow, deny []string
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Operation constraints (highest priority, above every exploration/expansion heuristic below; before generating any intent or executing any action you must first self-check for violations — if it violates a constraint, do not proceed)]:")
	if len(allow) > 0 {
		b.WriteString("\nAllowed operations:\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\nForbidden operations:\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n(Discovering a new target/port/host outside the constraints does not grant authorization: unless it falls within the allowed range above, record it as an out-of-scope fact and skip it — do not spawn intents or execute actions for it.)")
	return b.String()
}
