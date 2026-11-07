package agent

import (
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestExplicitConstraintLines(t *testing.T) {
	desc := "Target: example.com.\nAllowed: public sources only for example.com. Collect only with mcp__hexstrike__subfinder_scan.\nForbidden: any traffic to example.com. Do not collect with Bash.\n"
	got := ExplicitConstraintLines(desc, "Mode: recon")
	if len(got) != 2 || got[0].Kind != "allow" || got[1].Kind != "deny" {
		t.Fatalf("lines: %+v", got)
	}
	if got[0].Text == "" || got[1].Text == "" {
		t.Fatalf("empty body: %+v", got)
	}
	if ExplicitConstraintLines("just a goal", "") != nil {
		t.Fatal("prose should not become constraints")
	}
	lines := ExplicitConstraintLines(desc, "")
	missing := MissingConstraintLines([]db.Constraint{{Kind: "deny", Text: "any traffic to example.com. Do not collect with Bash."}}, lines)
	if len(missing) != 1 || missing[0].Kind != "allow" {
		t.Fatalf("allow should still be missing: %+v", missing)
	}
}
