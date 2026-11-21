package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseWorktrees(t *testing.T) {
	raw := strings.Join([]string{
		"worktree /Users/dev/projects/oneTrick",
		"HEAD f1608ec2ce4be22dc6165c0d02d6a8ca6235a837",
		"branch refs/heads/main",
		"",
		"worktree /Users/dev/projects/oneTrick.feat-issue-48",
		"HEAD 8a831a54a705539e4abda160e75e187ce25aade6",
		"branch refs/heads/feat/issue-48",
		"",
		"worktree /Users/dev/projects/oneTrick.detached",
		"HEAD 1234567890abcdef",
		"detached",
		"",
	}, "\n")

	wts := parseWorktrees(raw)
	if len(wts) != 3 {
		t.Fatalf("expected 3 worktrees, got %d", len(wts))
	}
	if !wts[0].IsMain || wts[0].Branch != "main" {
		t.Errorf("unexpected main worktree: %+v", wts[0])
	}
	if wts[1].IsMain || wts[1].Branch != "feat/issue-48" {
		t.Errorf("unexpected linked worktree: %+v", wts[1])
	}
	if !wts[2].Detached || wts[2].DisplayBranch() != "(detached @ 1234567)" {
		t.Errorf("unexpected detached worktree: %+v (%s)", wts[2], wts[2].DisplayBranch())
	}
}

func TestParseLsofListenersDeduplicatesDualStack(t *testing.T) {
	raw := strings.Join([]string{
		"p72139",
		"conetrick-server",
		"f8",
		"n*:8080",
		"f9",
		"n[::]:8080",
		"p80001",
		"cserver",
		"f4",
		"n127.0.0.1:8081",
		"f5",
		"n[::1]:9090",
	}, "\n")

	got := parseLsofListeners(raw)
	if len(got) != 3 {
		t.Fatalf("expected 3 listeners, got %d: %+v", len(got), got)
	}
	if got[0].PID != 72139 || got[0].Port != 8080 {
		t.Errorf("unexpected first listener: %+v", got[0])
	}
	if got[1].PID != 80001 || got[1].Port != 8081 {
		t.Errorf("unexpected second listener: %+v", got[1])
	}
	if got[2].PID != 80001 || got[2].Port != 9090 {
		t.Errorf("unexpected third listener: %+v", got[2])
	}
}

func TestParsePsOutput(t *testing.T) {
	raw := "  72139 02-14:20:01 /tmp/scratch/onetrick-server --flag value\n" +
		"80001   00:12 /Users/dev/My Projects/oneTrick/server\n"

	got := parsePsOutput(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[72139].Uptime != "02-14:20:01" || got[72139].Command != "/tmp/scratch/onetrick-server --flag value" {
		t.Errorf("unexpected psInfo for 72139: %+v", got[72139])
	}
	if got[80001].Uptime != "00:12" || got[80001].Command != "/Users/dev/My Projects/oneTrick/server" {
		t.Errorf("unexpected psInfo for 80001: %+v", got[80001])
	}
}

func TestMatchWorktreeRespectsPathBoundaries(t *testing.T) {
	wts := []Worktree{
		{Path: "/Users/dev/oneTrick", ResolvedPath: "/Users/dev/oneTrick", Branch: "main"},
		{Path: "/Users/dev/oneTrick.feat-x", ResolvedPath: "/Users/dev/oneTrick.feat-x", Branch: "feat/x"},
		{Path: "/Users/dev/oneTrick/.worktrees/nested", ResolvedPath: "/Users/dev/oneTrick/.worktrees/nested", Branch: "nested"},
	}

	// Sibling directory sharing prefix must not match main worktree.
	if _, ok := matchWorktree("/Users/dev/oneTrickUI", "node server.js", wts); ok {
		t.Fatal("expected oneTrickUI not to match oneTrick worktree")
	}

	wt, ok := matchWorktree("/Users/dev/oneTrick.feat-x", "/tmp/onetrick-server", wts)
	if !ok || wt.Branch != "feat/x" {
		t.Fatalf("expected feat/x match, got ok=%v wt=%+v", ok, wt)
	}

	// Nested worktree should win via longest prefix match.
	wt, ok = matchWorktree("/Users/dev/oneTrick/.worktrees/nested/subdir", "./server", wts)
	if !ok || wt.Branch != "nested" {
		t.Fatalf("expected nested match, got ok=%v wt=%+v", ok, wt)
	}

	// Executable inside worktree matches even when cwd is outside.
	wt, ok = matchWorktree("/tmp", "/Users/dev/oneTrick/server", wts)
	if !ok || wt.Branch != "main" {
		t.Fatalf("expected main match via executable path, got ok=%v wt=%+v", ok, wt)
	}
}

func TestBuildServerEntries(t *testing.T) {
	wts := []Worktree{
		{Path: "/Users/dev/oneTrick", ResolvedPath: "/Users/dev/oneTrick", Branch: "main", IsMain: true},
		{Path: "/Users/dev/oneTrick.feat-a", ResolvedPath: "/Users/dev/oneTrick.feat-a", Branch: "feat/a"},
		{Path: "/Users/dev/oneTrick.feat-b", ResolvedPath: "/Users/dev/oneTrick.feat-b", Branch: "feat/b"},
	}

	listeners := []listener{
		{PID: 101, Command: "server", Port: 8082, Address: "*:8082"},
		{PID: 102, Command: "onetrick-server", Port: 8080, Address: "127.0.0.1:8080"},
		{PID: 103, Command: "dlv", Port: 2345, Address: "127.0.0.1:2345"},
		{PID: 104, Command: "node", Port: 4200, Address: "*:4200"},
		{PID: 105, Command: "onetrick-server", Port: 8085, Address: "*:8085"},
	}

	cwds := map[int]string{
		101: "/Users/dev/oneTrick.feat-a",
		102: "/Users/dev/oneTrick",
		103: "/Users/dev/oneTrick",
		104: "/Users/dev/oneTrickUI",
		105: "/Users/dev/deleted-worktree",
	}

	psMap := map[int]psInfo{
		101: {PID: 101, Uptime: "01:00", Command: "/Users/dev/oneTrick.feat-a/server"},
		102: {PID: 102, Uptime: "05:00", Command: "/tmp/onetrick-server"},
		103: {PID: 103, Uptime: "02:00", Command: "/usr/local/bin/dlv dap"},
		104: {PID: 104, Uptime: "10:00", Command: "/usr/bin/node ng serve"},
		105: {PID: 105, Uptime: "12:00", Command: "/tmp/onetrick-server"},
	}

	runningOnly := buildServerEntries(wts, listeners, cwds, psMap, false)
	if len(runningOnly) != 3 {
		t.Fatalf("expected 3 running servers, got %d: %+v", len(runningOnly), runningOnly)
	}
	// Sorted by port ascending: 8080 (main), 8082 (feat/a), 8085 (untracked orphan).
	if runningOnly[0].Port != 8080 || runningOnly[0].Branch != "main" {
		t.Errorf("unexpected entry 0: %+v", runningOnly[0])
	}
	if runningOnly[1].Port != 8082 || runningOnly[1].Branch != "feat/a" {
		t.Errorf("unexpected entry 1: %+v", runningOnly[1])
	}
	if runningOnly[2].Port != 8085 || runningOnly[2].Branch != "(untracked)" {
		t.Errorf("unexpected entry 2: %+v", runningOnly[2])
	}

	withAll := buildServerEntries(wts, listeners, cwds, psMap, true)
	if len(withAll) != 4 {
		t.Fatalf("expected 4 entries with -all, got %d: %+v", len(withAll), withAll)
	}
	if withAll[3].Status != "stopped" || withAll[3].Branch != "feat/b" {
		t.Errorf("expected stopped feat/b entry last, got %+v", withAll[3])
	}
}

func TestPrintTable(t *testing.T) {
	var empty bytes.Buffer
	printTable(&empty, nil, "/Users/dev", false, false)
	if !strings.Contains(empty.String(), "No running oneTrick servers found.") {
		t.Errorf("unexpected empty output: %q", empty.String())
	}

	var buf bytes.Buffer
	entries := []ServerEntry{
		{
			Port:     8080,
			URL:      "http://localhost:8080",
			Address:  "*:8080",
			PID:      72139,
			Uptime:   "02-14:20:01",
			Branch:   "feat/issue-48",
			Worktree: "/Users/dev/projects/oneTrick.feat-issue-48",
			Status:   "running",
			Command:  "/tmp/onetrick-server",
		},
	}
	printTable(&buf, entries, "/Users/dev", false, false)
	out := buf.String()
	for _, want := range []string{"PORT", "URL", "PID", "UPTIME", "BRANCH", "WORKTREE", "8080", "http://localhost:8080", "72139", "feat/issue-48", "~/projects/oneTrick.feat-issue-48"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected table output to contain %q, got:\n%s", want, out)
		}
	}
}
