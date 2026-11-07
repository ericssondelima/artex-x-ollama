package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolGateAllowList(t *testing.T) {
	g := toolGate{allow: map[string]bool{"mcp__hexstrike__subfinder_scan": true}}
	if msg := g.reject("ExecuteExtraTool", json.RawMessage(`{"tool_name":"mcp__hexstrike__nmap_scan"}`)); !strings.Contains(msg, "nmap_scan") {
		t.Fatalf("nmap should be blocked: %q", msg)
	}
	if msg := g.reject("ExecuteExtraTool", json.RawMessage(`{"tool_name":"mcp__hexstrike__subfinder_scan"}`)); msg != "" {
		t.Fatalf("named tool should run: %q", msg)
	}
	if msg := g.reject("Bash", json.RawMessage(`{"command":"true"}`)); !strings.Contains(msg, "Bash") {
		t.Fatalf("bash should be blocked when an allow list exists: %q", msg)
	}
	if msg := g.reject("record_fact", json.RawMessage(`{}`)); msg != "" {
		t.Fatalf("record_fact should run: %q", msg)
	}
	if msg := g.reject("SearchExtraTools", json.RawMessage(`{"query":"select:mcp__hexstrike__nmap_scan"}`)); msg != "" {
		t.Fatalf("lookup should run: %q", msg)
	}
}

func TestToolGateDenyWithoutAllowList(t *testing.T) {
	g := toolGate{deny: map[string]bool{"Bash": true, "mcp__hexstrike__nmap_advanced_scan": true}}
	if msg := g.reject("Bash", nil); msg == "" {
		t.Fatal("bash deny missed")
	}
	if msg := g.reject("ExecuteExtraTool", json.RawMessage(`{"tool_name":"mcp__hexstrike__nmap_advanced_scan"}`)); msg == "" {
		t.Fatal("denied nmap missed")
	}
	if msg := g.reject("ExecuteExtraTool", json.RawMessage(`{"tool_name":"mcp__hexstrike__subfinder_scan"}`)); msg != "" {
		t.Fatalf("unlisted tool should run when there is no allow list: %q", msg)
	}
}

func TestRejectIntentOutsideAllow(t *testing.T) {
	g := toolGate{allow: map[string]bool{"mcp__hexstrike__subfinder_scan": true}}
	if msg := g.rejectIntent("Use mcp__hexstrike__dirsearch_scan on example.com"); !strings.Contains(msg, "dirsearch") {
		t.Fatalf("outside tool: %q", msg)
	}
	if msg := g.rejectIntent("Use mcp__hexstrike__subfinder_scan"); msg != "" {
		t.Fatalf("allowed tool: %q", msg)
	}
	if msg := g.rejectIntent("prove_goal"); msg == "" {
		t.Fatal("prove_goal intent should be rejected")
	}
}

func TestToolGateOff(t *testing.T) {
	if msg := (toolGate{}).reject("Bash", nil); msg != "" {
		t.Fatalf("empty gate blocked: %q", msg)
	}
}
