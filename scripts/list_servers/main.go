package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

type Worktree struct {
	Path         string
	ResolvedPath string
	Head         string
	Branch       string
	Detached     bool
	Bare         bool
	IsMain       bool
}

type listener struct {
	PID     int
	Command string
	Port    int
	Address string
}

type psInfo struct {
	PID     int
	Uptime  string
	Command string
}

type ServerEntry struct {
	Port     int    `json:"port,omitempty"`
	URL      string `json:"url,omitempty"`
	Address  string `json:"address,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Uptime   string `json:"uptime,omitempty"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	Status   string `json:"status"`
	Command  string `json:"command,omitempty"`
}

var ignoredExecutables = map[string]bool{
	"dlv":                   true,
	"gopls":                 true,
	"goland":                true,
	"webstorm":              true,
	"jetbrains-toolbox":     true,
	"localharness_external": true,
	"agy_acp_server":        true,
	"cef_server":            true,
	"code":                  true,
	"cursor":                true,
	"docker":                true,
	"com.docker.backend":    true,
}

func main() {
	showAll := flag.Bool("all", false, "include worktrees with no running server")
	asJSON := flag.Bool("json", false, "output results as JSON")
	verbose := flag.Bool("v", false, "include bind address and full command columns")
	flag.Parse()

	entries, err := discoverServers(*showAll)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
			os.Exit(1)
		}
		return
	}

	homeDir, _ := os.UserHomeDir()
	printTable(os.Stdout, entries, homeDir, *showAll, *verbose)
}

func discoverServers(includeStopped bool) ([]ServerEntry, error) {
	wtOut, err := runCmd("git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("failed to list git worktrees: %w", err)
	}
	worktrees := parseWorktrees(wtOut)
	for i := range worktrees {
		worktrees[i].ResolvedPath = resolvePath(worktrees[i].Path)
	}

	lsofOut, err := runLsofListeners()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect listening sockets via lsof: %w", err)
	}
	listeners := parseLsofListeners(lsofOut)

	pidSet := make(map[int]struct{}, len(listeners))
	pids := make([]int, 0, len(listeners))
	for _, l := range listeners {
		if _, seen := pidSet[l.PID]; !seen {
			pidSet[l.PID] = struct{}{}
			pids = append(pids, l.PID)
		}
	}

	var cwds map[int]string
	var psMap map[int]psInfo
	if len(pids) > 0 {
		pidArg := joinInts(pids)
		cwdOut, _ := runCmdAllowExit1("lsof", "-a", "-p", pidArg, "-d", "cwd", "-Fn")
		cwds = parseLsofCwds(cwdOut)

		psOut, _ := runCmdAllowExit1("ps", "-ww", "-p", pidArg, "-o", "pid=,etime=,command=")
		psMap = parsePsOutput(psOut)
	}

	return buildServerEntries(worktrees, listeners, cwds, psMap, includeStopped), nil
}

func buildServerEntries(
	worktrees []Worktree,
	listeners []listener,
	cwds map[int]string,
	psMap map[int]psInfo,
	includeStopped bool,
) []ServerEntry {
	activeWorktrees := make(map[string]bool)
	entries := make([]ServerEntry, 0)

	for _, l := range listeners {
		info := psMap[l.PID]
		cwd := cwds[l.PID]
		fullCmd := info.Command
		if fullCmd == "" {
			fullCmd = l.Command
		}

		if isIgnoredProcess(l.Command, fullCmd) {
			continue
		}

		wt, matched := matchWorktree(cwd, fullCmd, worktrees)
		if matched {
			activeWorktrees[wt.Path] = true
			entries = append(entries, ServerEntry{
				Port:     l.Port,
				URL:      formatURL(l.Address, l.Port),
				Address:  l.Address,
				PID:      l.PID,
				Uptime:   info.Uptime,
				Branch:   wt.DisplayBranch(),
				Worktree: wt.Path,
				Status:   "running",
				Command:  fullCmd,
			})
			continue
		}

		if isOrphanOneTrickProcess(l.Command, fullCmd) {
			worktreePath := cwd
			if worktreePath == "" {
				worktreePath = "(unknown)"
			}
			entries = append(entries, ServerEntry{
				Port:     l.Port,
				URL:      formatURL(l.Address, l.Port),
				Address:  l.Address,
				PID:      l.PID,
				Uptime:   info.Uptime,
				Branch:   "(untracked)",
				Worktree: worktreePath,
				Status:   "running",
				Command:  fullCmd,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Port != entries[j].Port {
			return entries[i].Port < entries[j].Port
		}
		return entries[i].PID < entries[j].PID
	})

	if includeStopped {
		stopped := make([]ServerEntry, 0)
		for _, wt := range worktrees {
			if wt.Bare || activeWorktrees[wt.Path] {
				continue
			}
			stopped = append(stopped, ServerEntry{
				Branch:   wt.DisplayBranch(),
				Worktree: wt.Path,
				Status:   "stopped",
			})
		}
		entries = append(entries, stopped...)
	}

	return entries
}

func (w Worktree) DisplayBranch() string {
	if w.Branch != "" {
		return w.Branch
	}
	if w.Bare {
		return "(bare)"
	}
	if w.Head != "" {
		short := w.Head
		if len(short) > 7 {
			short = short[:7]
		}
		return fmt.Sprintf("(detached @ %s)", short)
	}
	return "(unknown)"
}

func parseWorktrees(output string) []Worktree {
	var worktrees []Worktree
	var current *Worktree

	flush := func() {
		if current != nil && current.Path != "" {
			if len(worktrees) == 0 {
				current.IsMain = true
			}
			worktrees = append(worktrees, *current)
		}
		current = nil
	}

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			flush()
			p := strings.TrimPrefix(line, "worktree ")
			current = &Worktree{Path: p, ResolvedPath: p}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "bare":
			current.Bare = true
		}
	}
	flush()
	return worktrees
}

func parseLsofListeners(output string) []listener {
	var listeners []listener
	var currentPID int
	var currentCmd string
	seen := make(map[string]bool)

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if len(line) < 2 {
			continue
		}
		prefix := line[0]
		value := line[1:]
		switch prefix {
		case 'p':
			pid, err := strconv.Atoi(value)
			if err == nil {
				currentPID = pid
				currentCmd = ""
			}
		case 'c':
			currentCmd = value
		case 'n':
			if currentPID == 0 {
				continue
			}
			port, ok := extractPort(value)
			if !ok {
				continue
			}
			key := fmt.Sprintf("%d:%d", currentPID, port)
			if seen[key] {
				continue
			}
			seen[key] = true
			listeners = append(listeners, listener{
				PID:     currentPID,
				Command: currentCmd,
				Port:    port,
				Address: value,
			})
		}
	}
	return listeners
}

func parseLsofCwds(output string) map[int]string {
	cwds := make(map[int]string)
	var currentPID int

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			if pid, err := strconv.Atoi(line[1:]); err == nil {
				currentPID = pid
			}
		case 'n':
			if currentPID != 0 {
				cwds[currentPID] = line[1:]
			}
		}
	}
	return cwds
}

func parsePsOutput(output string) map[int]psInfo {
	result := make(map[int]psInfo)
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		pidStr, rest, ok := cutField(line)
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue
		}
		uptime, cmd, ok := cutField(rest)
		if !ok {
			continue
		}
		result[pid] = psInfo{
			PID:     pid,
			Uptime:  uptime,
			Command: strings.TrimSpace(cmd),
		}
	}
	return result
}

func cutField(s string) (field, rest string, ok bool) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", "", false
	}
	idx := strings.IndexAny(s, " \t")
	if idx == -1 {
		return s, "", true
	}
	return s[:idx], strings.TrimLeft(s[idx:], " \t"), true
}

func extractPort(addr string) (int, bool) {
	idx := strings.LastIndex(addr, ":")
	if idx == -1 || idx == len(addr)-1 {
		return 0, false
	}
	port, err := strconv.Atoi(addr[idx+1:])
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}

func formatURL(addr string, port int) string {
	idx := strings.LastIndex(addr, ":")
	host := ""
	if idx > 0 {
		host = addr[:idx]
	}
	switch host {
	case "", "*", "0.0.0.0", "[::]", "::", "127.0.0.1", "[::1]", "::1":
		return fmt.Sprintf("http://localhost:%d", port)
	default:
		return fmt.Sprintf("http://%s:%d", host, port)
	}
}

func isIgnoredProcess(lsofCmd, fullCmd string) bool {
	if ignoredExecutables[strings.ToLower(lsofCmd)] {
		return true
	}
	exe, _, _ := cutField(fullCmd)
	base := strings.ToLower(filepath.Base(exe))
	return ignoredExecutables[base]
}

func isOrphanOneTrickProcess(lsofCmd, fullCmd string) bool {
	if strings.Contains(strings.ToLower(lsofCmd), "onetrick") {
		return true
	}
	exe, _, _ := cutField(fullCmd)
	return strings.Contains(strings.ToLower(filepath.Base(exe)), "onetrick")
}

func matchWorktree(cwd, fullCmd string, worktrees []Worktree) (Worktree, bool) {
	if cwd != "" {
		if wt, ok := longestMatchingWorktree(resolvePath(cwd), worktrees); ok {
			return wt, true
		}
	}
	if fullCmd != "" {
		exe, _, _ := cutField(fullCmd)
		if wt, ok := longestMatchingWorktree(resolvePath(exe), worktrees); ok {
			return wt, true
		}
		if wt, ok := longestCommandPrefixWorktree(fullCmd, worktrees); ok {
			return wt, true
		}
	}
	return Worktree{}, false
}

func longestMatchingWorktree(target string, worktrees []Worktree) (Worktree, bool) {
	var best Worktree
	bestLen := -1
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		if isPathWithin(target, wt.ResolvedPath) && len(wt.ResolvedPath) > bestLen {
			best = wt
			bestLen = len(wt.ResolvedPath)
		}
	}
	return best, bestLen >= 0
}

func longestCommandPrefixWorktree(fullCmd string, worktrees []Worktree) (Worktree, bool) {
	var best Worktree
	bestLen := -1
	sep := string(os.PathSeparator)
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		for _, candidate := range []string{wt.Path, wt.ResolvedPath} {
			if candidate == "" {
				continue
			}
			prefix := candidate + sep
			if strings.HasPrefix(fullCmd, prefix) && len(candidate) > bestLen {
				best = wt
				bestLen = len(candidate)
			}
		}
	}
	return best, bestLen >= 0
}

func isPathWithin(path, base string) bool {
	if path == "" || base == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	cleanBase := filepath.Clean(base)
	if cleanPath == cleanBase {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanBase+string(os.PathSeparator))
}

func resolvePath(p string) string {
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

func shortenHome(p, homeDir string) string {
	if homeDir != "" && isPathWithin(p, homeDir) {
		if p == homeDir {
			return "~"
		}
		return "~" + strings.TrimPrefix(p, homeDir)
	}
	return p
}

func printTable(w io.Writer, entries []ServerEntry, homeDir string, showAll, verbose bool) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No running oneTrick servers found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	headers := []string{"PORT", "URL", "PID", "UPTIME"}
	if showAll {
		headers = append(headers, "STATUS")
	}
	headers = append(headers, "BRANCH", "WORKTREE")
	if verbose {
		headers = append(headers, "ADDRESS", "COMMAND")
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))

	for _, e := range entries {
		portStr := "-"
		if e.Port > 0 {
			portStr = strconv.Itoa(e.Port)
		}
		urlStr := dashIfEmpty(e.URL)
		pidStr := "-"
		if e.PID > 0 {
			pidStr = strconv.Itoa(e.PID)
		}
		uptimeStr := dashIfEmpty(e.Uptime)

		cols := []string{portStr, urlStr, pidStr, uptimeStr}
		if showAll {
			cols = append(cols, e.Status)
		}
		cols = append(cols, e.Branch, shortenHome(e.Worktree, homeDir))
		if verbose {
			cols = append(cols, dashIfEmpty(e.Address), dashIfEmpty(e.Command))
		}
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}
	_ = tw.Flush()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func joinInts(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func runLsofListeners() (string, error) {
	return runCmdAllowExit1("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn")
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return stdout.String(), nil
}

// runCmdAllowExit1 treats exit status 1 with no stderr as an empty result (e.g. lsof finding no matches).
func runCmdAllowExit1(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(stderr.String()) == "" {
			return stdout.String(), nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return stdout.String(), nil
}
