package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/url"
	"oneTrick/logger"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// GCPLogEntry represents a single entry returned by `gcloud logging read --format=json`.
type GCPLogEntry struct {
	InsertID         string            `json:"insertId"`
	LogName          string            `json:"logName"`
	Severity         string            `json:"severity"`
	Timestamp        string            `json:"timestamp"`
	ReceiveTimestamp string            `json:"receiveTimestamp"`
	TextPayload      string            `json:"textPayload,omitempty"`
	JSONPayload      map[string]any    `json:"jsonPayload,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Resource         struct {
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	} `json:"resource"`
	HTTPRequest *struct {
		RequestMethod string `json:"requestMethod"`
		RequestURL    string `json:"requestUrl"`
		Status        int    `json:"status"`
		Latency       string `json:"latency"`
		UserAgent     string `json:"userAgent"`
		RemoteIP      string `json:"remoteIp"`
	} `json:"httpRequest,omitempty"`
}

// SourceLocation identifies a handler or service method in the repository.
type SourceLocation struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// SampleCall holds details of an individual anomalous request or background task.
type SampleCall struct {
	Timestamp     string                 `json:"timestamp"`
	InsertID      string                 `json:"insertId"`
	Severity      string                 `json:"severity"`
	Status        int                    `json:"status,omitempty"`
	Method        string                 `json:"method,omitempty"`
	Path          string                 `json:"path,omitempty"`
	Latency       string                 `json:"latency,omitempty"`
	LatencyMs     float64                `json:"latencyMs,omitempty"`
	ErrorMsg      string                 `json:"errorMsg,omitempty"`
	StackTrace    string                 `json:"stackTrace,omitempty"`
	Revision      string                 `json:"revision,omitempty"`
	CommitSHA     string                 `json:"commitSha,omitempty"`
	RequestFields map[string]any         `json:"requestFields,omitempty"`
	ActorFields   map[string]any         `json:"actorFields,omitempty"`
	ServiceGroups map[string]any         `json:"serviceGroups,omitempty"`
	ParentFields  map[string]any         `json:"parentFields,omitempty"`
	Warnings      []string               `json:"warnings,omitempty"`
	RawExtra      map[string]any         `json:"rawExtra,omitempty"`
}

// IssueGroup aggregates occurrences of the same error or slow operation.
type IssueGroup struct {
	Signature       string           `json:"signature"`
	Category        string           `json:"category"` // "ERROR", "SLOW", or "ERROR+SLOW"
	Event           string           `json:"event"`
	OperationID     string           `json:"operationId,omitempty"`
	JobName         string           `json:"jobName,omitempty"`
	Method          string           `json:"method,omitempty"`
	RoutePattern    string           `json:"routePattern,omitempty"`
	Status          int              `json:"status,omitempty"`
	ErrorMessage    string           `json:"errorMessage,omitempty"`
	Count           int              `json:"count"`
	ErrorCount      int              `json:"errorCount"`
	SlowCount       int              `json:"slowCount"`
	FirstSeen       string           `json:"firstSeen"`
	LastSeen        string           `json:"lastSeen"`
	MinLatencyMs    float64          `json:"minLatencyMs"`
	AvgLatencyMs    float64          `json:"avgLatencyMs"`
	P95LatencyMs    float64          `json:"p95LatencyMs"`
	MaxLatencyMs    float64          `json:"maxLatencyMs"`
	HandlerLocation *SourceLocation  `json:"handlerLocation,omitempty"`
	ServiceLocations []SourceLocation `json:"serviceLocations,omitempty"`
	Revisions       []string         `json:"revisions,omitempty"`
	CommitSHAs      []string         `json:"commitShas,omitempty"`
	Samples         []SampleCall     `json:"samples"`

	latencies []float64
}

// MonitorReport is the top-level structured output written to JSON and rendered to Markdown.
type MonitorReport struct {
	GeneratedAt          string       `json:"generatedAt"`
	ProjectID            string       `json:"projectId"`
	ServiceName          string       `json:"serviceName"`
	WindowSince          string       `json:"windowSince"`
	WindowStartUTC       string       `json:"windowStartUtc"`
	SlowThreshold        string       `json:"slowThreshold"`
	Include4xx           bool         `json:"include4xx"`
	TotalEntriesFetched  int          `json:"totalEntriesFetched"`
	SuppressedScanner404 int          `json:"suppressedScanner404"`
	TotalFlaggedCalls    int          `json:"totalFlaggedCalls"`
	TotalErrorGroups     int          `json:"totalErrorGroups"`
	TotalSlowGroups      int          `json:"totalSlowGroups"`
	IssueGroups          []IssueGroup `json:"issueGroups"`
}

type sourceIndex struct {
	handlersByEvent map[string]SourceLocation
	jobsByName      map[string]SourceLocation
	serviceGroups   map[string]SourceLocation
}

var (
	funcServerRegex = regexp.MustCompile(`^func\s+\([a-zA-Z0-9_]+\s+\*?Server\)\s+([A-Za-z0-9_]+)\s*\(`)
	clogGoRegex     = regexp.MustCompile(`clog\.Go\([^,]+,\s*"([^"]+)"`)
	clogGroupRegex  = regexp.MustCompile(`clog\.WithGroup(?:Field|Fields)\([^,]+,\s*"([^"]+)"`)
	numericIDRegex  = regexp.MustCompile(`^[0-9]{4,}$`)
	uuidRegex       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	alnumIDRegex    = regexp.MustCompile(`^[A-Za-z0-9]{15,}$`)
)

func main() {
	_ = godotenv.Load()

	defaultProject := os.Getenv("GCP_PROJECT_ID")
	if defaultProject == "" {
		defaultProject = "gruntt-destiny"
	}

	var (
		projectID          = flag.String("project", defaultProject, "GCP Project ID")
		serviceName        = flag.String("service", "onetrick", "Cloud Run service name")
		sinceStr           = flag.String("since", "24h", "Lookback window duration (e.g. 1h, 24h, 72h, 7d)")
		slowStr            = flag.String("slow", "2s", "Latency threshold for long-running calls (e.g. 500ms, 1s, 2s)")
		include4xx         = flag.Bool("include-4xx", false, "Include 4xx responses/warnings in the report")
		includeScanner404s = flag.Bool("include-scanner-404s", false, "Include validate-request-failure 404s from unknown scanner routes")
		includeProxySlow   = flag.Bool("include-proxy-slow", false, "Include Cloud Run proxy slow requests (e.g. container cold starts)")
		limit              = flag.Int("limit", 500, "Maximum number of log entries to fetch from Cloud Logging")
		outMD              = flag.String("out-md", "reports/gcp-log-monitor-latest.md", "Output path for Markdown report")
		outJSON            = flag.String("out-json", "reports/gcp-log-monitor-latest.json", "Output path for JSON report")
		inputFile          = flag.String("input-json", "", "Optional path to pre-fetched gcloud JSON logs (skips gcloud call)")
		failOnIssues       = flag.Bool("fail-on-issues", false, "Exit with status 1 if any errors or slow calls are found")
	)
	flag.Parse()

	sinceDur, err := parseLookback(*sinceStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid -since value %q: %v\n", *sinceStr, err)
		os.Exit(2)
	}
	slowThreshold, err := time.ParseDuration(*slowStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid -slow value %q: %v\n", *slowStr, err)
		os.Exit(2)
	}

	now := time.Now().UTC()
	cutoff := now.Add(-sinceDur)

	repoRoot, _ := os.Getwd()
	idx := buildSourceIndex(repoRoot)

	var entries []GCPLogEntry
	if *inputFile != "" {
		raw, err := os.ReadFile(*inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read -input-json %q: %v\n", *inputFile, err)
			os.Exit(2)
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse -input-json %q: %v\n", *inputFile, err)
			os.Exit(2)
		}
	} else {
		filter := buildGCPFilter(*serviceName, cutoff, slowThreshold, *include4xx, *includeProxySlow)
		fmt.Printf("Querying GCP Cloud Logging (project=%s, service=%s, since=%s, slow>=%s)...\n",
			*projectID, *serviceName, *sinceStr, slowThreshold)

		entries, err = fetchGCPLogs(context.Background(), *projectID, filter, *limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error querying GCP logs: %v\n", err)
			os.Exit(1)
		}
	}

	report := analyzeEntries(entries, idx, *projectID, *serviceName, *sinceStr, cutoff, slowThreshold, *include4xx, *includeScanner404s, now)

	if *outJSON != "" {
		if err := writeJSONReport(*outJSON, report); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write JSON report to %s: %v\n", *outJSON, err)
			os.Exit(1)
		}
	}
	if *outMD != "" {
		md := renderMarkdownReport(report, repoRoot)
		if err := writeTextFile(*outMD, md); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write Markdown report to %s: %v\n", *outMD, err)
			os.Exit(1)
		}
	}

	printConsoleSummary(report, *outMD, *outJSON)

	if *failOnIssues && len(report.IssueGroups) > 0 {
		os.Exit(1)
	}
}

// parseLookback supports standard Go durations plus "Xd" for days.
func parseLookback(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		var days float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%f", &days); err == nil && days > 0 {
			return time.Duration(days * float64(24*time.Hour)), nil
		}
	}
	return time.ParseDuration(s)
}

// buildGCPFilter constructs a Cloud Logging filter that pushes as much filtering as possible to GCP.
func buildGCPFilter(serviceName string, cutoff time.Time, slowThreshold time.Duration, include4xx, includeProxySlow bool) string {
	base := fmt.Sprintf(
		`resource.type="cloud_run_revision" AND resource.labels.service_name=%q AND timestamp>=%q`,
		serviceName,
		cutoff.Format(time.RFC3339),
	)

	minSeverity := "ERROR"
	if include4xx {
		minSeverity = "WARNING"
	}

	// In Go's time.Duration.String(), any duration >= 1s ends with a digit followed by "s" (e.g. "2.41s", "1m2s"),
	// whereas sub-second durations end with "ms", "µs", or "ns".
	var slowClause string
	if slowThreshold >= time.Second {
		slowClause = `jsonPayload.latency=~"[0-9]s$" OR jsonPayload.duration=~"[0-9]s$"`
	} else {
		slowClause = `jsonPayload.latency=~"[0-9](m?s)$" OR jsonPayload.duration=~"[0-9](m?s)$"`
	}

	httpClause := `(logName=~"run.googleapis.com%2Frequests$" AND httpRequest.status>=500)`
	if includeProxySlow {
		httpSlowSec := fmt.Sprintf("%.3fs", slowThreshold.Seconds())
		httpClause = fmt.Sprintf(`(logName=~"run.googleapis.com%%2Frequests$" AND (httpRequest.status>=500 OR httpRequest.latency>=%q))`, httpSlowSec)
	}

	return fmt.Sprintf(`%s AND ((labels.logger="onetrick-service" AND (severity>=%s OR %s)) OR %s)`,
		base, minSeverity, slowClause, httpClause)
}

func fetchGCPLogs(ctx context.Context, projectID, filter string, limit int) ([]GCPLogEntry, error) {
	args := []string{
		"logging", "read", filter,
		"--project=" + projectID,
		fmt.Sprintf("--limit=%d", limit),
		"--format=json",
	}
	cmd := exec.CommandContext(ctx, "gcloud", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gcloud logging read failed: %w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}

	var entries []GCPLogEntry
	if stdout.Len() == 0 {
		return entries, nil
	}
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		return nil, fmt.Errorf("failed to unmarshal gcloud output: %w", err)
	}
	return entries, nil
}

var reservedPayloadKeys = map[string]bool{
	"ip":                           true,
	"latency":                      true,
	"duration":                     true,
	"message":                      true,
	"method":                       true,
	"path":                         true,
	"status":                       true,
	"user-agent":                   true,
	"job":                          true,
	"time":                         true,
	"severity":                     true,
	"request":                      true,
	"actor":                        true,
	"error":                        true,
	"parent":                       true,
	"logging.googleapis.com/labels": true,
}

func analyzeEntries(
	entries []GCPLogEntry,
	idx sourceIndex,
	projectID, serviceName, sinceStr string,
	cutoff time.Time,
	slowThreshold time.Duration,
	include4xx, includeScanner404s bool,
	now time.Time,
) MonitorReport {
	report := MonitorReport{
		GeneratedAt:         now.Format(time.RFC3339),
		ProjectID:           projectID,
		ServiceName:         serviceName,
		WindowSince:         sinceStr,
		WindowStartUTC:      cutoff.Format(time.RFC3339),
		SlowThreshold:       slowThreshold.String(),
		Include4xx:          include4xx,
		TotalEntriesFetched: len(entries),
		IssueGroups:         []IssueGroup{},
	}

	// Track timestamps/paths covered by canonical application logs so we don't double-report
	// the same HTTP request from Cloud Run's proxy log unless the app crashed or timed out before logging.
	canonicalCovered := make(map[string]bool)
	for _, e := range entries {
		if e.Labels["logger"] == "onetrick-service" && e.JSONPayload != nil {
			method, _ := e.JSONPayload["method"].(string)
			path, _ := e.JSONPayload["path"].(string)
			if method != "" && path != "" {
				ts, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
				bucket := ts.Unix() / 5
				canonicalCovered[fmt.Sprintf("%s:%s:%d", method, stripQuery(path), bucket)] = true
				canonicalCovered[fmt.Sprintf("%s:%s:%d", method, stripQuery(path), bucket-1)] = true
				canonicalCovered[fmt.Sprintf("%s:%s:%d", method, stripQuery(path), bucket+1)] = true
			}
		}
	}

	groupsByKey := make(map[string]*IssueGroup)

	for _, e := range entries {
		isCanonical := e.Labels["logger"] == "onetrick-service" && e.JSONPayload != nil

		var (
			event         string
			jobName       string
			method        string
			rawPath       string
			status        int
			latencyDur    time.Duration
			latencyStr    string
			errMsg        string
			stackTrace    string
			requestFields map[string]any
			actorFields   map[string]any
			parentFields  map[string]any
			serviceGroups = make(map[string]any)
			warnings      []string
		)

		if isCanonical {
			p := e.JSONPayload
			event, _ = p["message"].(string)
			jobName, _ = p["job"].(string)
			method, _ = p["method"].(string)
			rawPath, _ = p["path"].(string)
			status = toInt(p["status"])

			if l, ok := p["latency"].(string); ok && l != "" {
				latencyStr = l
				latencyDur, _ = time.ParseDuration(l)
			} else if d, ok := p["duration"].(string); ok && d != "" {
				latencyStr = d
				latencyDur, _ = time.ParseDuration(d)
			}

			if errGroup, ok := p["error"].(map[string]any); ok {
				errMsg, _ = errGroup["err"].(string)
				if errMsg == "" {
					errMsg, _ = errGroup["message"].(string)
				}
				stackTrace, _ = errGroup["stackTrace"].(string)
			}
			if reqGroup, ok := p["request"].(map[string]any); ok {
				requestFields = reqGroup
				if w, ok := reqGroup["warning"].(string); ok && w != "" {
					warnings = append(warnings, "request: "+w)
				}
			}
			if actGroup, ok := p["actor"].(map[string]any); ok {
				actorFields = actGroup
			}
			if parGroup, ok := p["parent"].(map[string]any); ok {
				parentFields = parGroup
			}

			for k, v := range p {
				if reservedPayloadKeys[k] {
					continue
				}
				serviceGroups[k] = v
				if m, ok := v.(map[string]any); ok {
					for subK, subV := range m {
						if subM, ok := subV.(map[string]any); ok {
							if w, ok := subM["warning"].(string); ok && w != "" {
								warnings = append(warnings, fmt.Sprintf("%s.%s: %s", k, subK, w))
							}
						}
					}
				}
			}
		} else if e.HTTPRequest != nil {
			// Cloud Run proxy log fallback (only if not already covered by canonical logger)
			method = e.HTTPRequest.RequestMethod
			rawPath = extractURLPath(e.HTTPRequest.RequestURL)
			status = e.HTTPRequest.Status
			latencyStr = e.HTTPRequest.Latency
			latencyDur, _ = time.ParseDuration(latencyStr)

			ts, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
			bucket := ts.Unix() / 5
			if canonicalCovered[fmt.Sprintf("%s:%s:%d", method, stripQuery(rawPath), bucket)] {
				continue
			}
			event = "cloud-run-http-request"
			if e.TextPayload != "" {
				errMsg = strings.TrimSpace(e.TextPayload)
			} else if status >= 500 {
				errMsg = fmt.Sprintf("Cloud Run HTTP %d (unhandled / proxy error)", status)
			}
		} else {
			continue
		}

		// Filter out noisy bot-scanner 404s ("validate-request-failure" with "no matching operation was found")
		if event == "validate-request-failure" && status == 404 && strings.Contains(errMsg, "no matching operation was found") {
			if !includeScanner404s {
				report.SuppressedScanner404++
				continue
			}
		}

		isSlow := latencyDur >= slowThreshold
		isError := status >= 500 ||
			(e.Severity == "ERROR" && (status == 0 || status >= 500 || include4xx)) ||
			strings.HasSuffix(event, "-failure") && (status == 0 || status >= 500 || include4xx)
		isWarn4xx := include4xx && (status >= 400 || e.Severity == "WARNING" || len(warnings) > 0)

		if !isSlow && !isError && !isWarn4xx {
			continue
		}

		category := "SLOW"
		if isError && isSlow {
			category = "ERROR+SLOW"
		} else if isError {
			category = "ERROR"
		} else if isWarn4xx && !isSlow {
			category = "WARN/4XX"
		}

		baseEvent := strings.TrimSuffix(strings.TrimSuffix(event, "-failure"), "-success")
		routePattern := normalizeRoute(rawPath)
		if jobName != "" && method == "" {
			method = "BG_JOB"
			routePattern = jobName
		}

		normErr := normalizeErrorMessage(errMsg)
		sig := fmt.Sprintf("%s|%s|%s|%d|%s", baseEvent, method, routePattern, status, normErr)

		grp, exists := groupsByKey[sig]
		if !exists {
			grp = &IssueGroup{
				Signature:    sig,
				Category:     category,
				Event:        event,
				JobName:      jobName,
				Method:       method,
				RoutePattern: routePattern,
				Status:       status,
				ErrorMessage: errMsg,
				FirstSeen:    e.Timestamp,
				LastSeen:     e.Timestamp,
			}
			if loc, ok := idx.handlersByEvent[baseEvent]; ok {
				locCopy := loc
				grp.OperationID = loc.Symbol
				grp.HandlerLocation = &locCopy
			} else if loc, ok := idx.jobsByName[baseEvent]; ok {
				locCopy := loc
				grp.HandlerLocation = &locCopy
			}
			groupsByKey[sig] = grp
		} else {
			if (category == "ERROR" && grp.Category == "SLOW") || (category == "SLOW" && grp.Category == "ERROR") || category == "ERROR+SLOW" {
				grp.Category = "ERROR+SLOW"
			}
			if e.Timestamp < grp.FirstSeen {
				grp.FirstSeen = e.Timestamp
			}
			if e.Timestamp > grp.LastSeen {
				grp.LastSeen = e.Timestamp
			}
		}

		grp.Count++
		if isError || isWarn4xx {
			grp.ErrorCount++
		}
		if isSlow {
			grp.SlowCount++
		}

		latMs := float64(latencyDur.Microseconds()) / 1000.0
		if latMs > 0 {
			grp.latencies = append(grp.latencies, latMs)
		}

		rev := e.Resource.Labels["revision_name"]
		if rev != "" && !slices.Contains(grp.Revisions, rev) {
			grp.Revisions = append(grp.Revisions, rev)
		}
		sha := e.Labels["commit-sha"]
		if sha != "" && !slices.Contains(grp.CommitSHAs, sha) {
			grp.CommitSHAs = append(grp.CommitSHAs, sha)
		}

		// Match service locations from serviceGroups
		for svcKey, svcVal := range serviceGroups {
			if m, ok := svcVal.(map[string]any); ok {
				for methodKey := range m {
					fullGroup := svcKey + "." + methodKey
					if loc, ok := idx.serviceGroups[fullGroup]; ok {
						if !containsLocation(grp.ServiceLocations, loc) {
							grp.ServiceLocations = append(grp.ServiceLocations, loc)
						}
					}
				}
			}
		}

		if len(grp.Samples) < 3 {
			var svcGroupsOut map[string]any
			if len(serviceGroups) > 0 {
				svcGroupsOut = serviceGroups
			}
			grp.Samples = append(grp.Samples, SampleCall{
				Timestamp:     e.Timestamp,
				InsertID:      e.InsertID,
				Severity:      e.Severity,
				Status:        status,
				Method:        method,
				Path:          rawPath,
				Latency:       latencyStr,
				LatencyMs:     math.Round(latMs*100) / 100,
				ErrorMsg:      errMsg,
				StackTrace:    stackTrace,
				Revision:      rev,
				CommitSHA:     sha,
				RequestFields: requestFields,
				ActorFields:   actorFields,
				ServiceGroups: svcGroupsOut,
				ParentFields:  parentFields,
				Warnings:      warnings,
			})
		}

		report.TotalFlaggedCalls++
	}

	for _, grp := range groupsByKey {
		if len(grp.latencies) > 0 {
			sort.Float64s(grp.latencies)
			grp.MinLatencyMs = math.Round(grp.latencies[0]*100) / 100
			grp.MaxLatencyMs = math.Round(grp.latencies[len(grp.latencies)-1]*100) / 100
			sum := 0.0
			for _, v := range grp.latencies {
				sum += v
			}
			grp.AvgLatencyMs = math.Round((sum/float64(len(grp.latencies)))*100) / 100
			p95Idx := int(math.Ceil(0.95*float64(len(grp.latencies)))) - 1
			if p95Idx < 0 {
				p95Idx = 0
			}
			grp.P95LatencyMs = math.Round(grp.latencies[p95Idx]*100) / 100
		}
		if strings.Contains(grp.Category, "ERROR") || grp.Category == "WARN/4XX" {
			report.TotalErrorGroups++
		}
		if strings.Contains(grp.Category, "SLOW") {
			report.TotalSlowGroups++
		}
		report.IssueGroups = append(report.IssueGroups, *grp)
	}

	// Sort: ERROR+SLOW first, then ERROR, then SLOW, then by Count desc, then MaxLatencyMs desc
	sort.Slice(report.IssueGroups, func(i, j int) bool {
		a, b := report.IssueGroups[i], report.IssueGroups[j]
		rankA, rankB := categoryRank(a.Category), categoryRank(b.Category)
		if rankA != rankB {
			return rankA < rankB
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.MaxLatencyMs > b.MaxLatencyMs
	})

	return report
}

func categoryRank(cat string) int {
	switch cat {
	case "ERROR+SLOW":
		return 1
	case "ERROR":
		return 2
	case "SLOW":
		return 3
	default:
		return 4
	}
}

func containsLocation(list []SourceLocation, target SourceLocation) bool {
	for _, item := range list {
		if item.Symbol == target.Symbol && item.File == target.File {
			return true
		}
	}
	return false
}

// buildSourceIndex scans impl.go and services/**/*.go to map clog events and service groups to files and line numbers.
func buildSourceIndex(repoRoot string) sourceIndex {
	idx := sourceIndex{
		handlersByEvent: make(map[string]SourceLocation),
		jobsByName:      make(map[string]SourceLocation),
		serviceGroups:   make(map[string]SourceLocation),
	}

	implPath := filepath.Join(repoRoot, "impl.go")
	scanFileForSourceIndex(implPath, "impl.go", &idx)

	servicesDir := filepath.Join(repoRoot, "services")
	_ = filepath.WalkDir(servicesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			rel = path
		}
		scanFileForSourceIndex(path, filepath.ToSlash(rel), &idx)
		return nil
	})

	return idx
}

func scanFileForSourceIndex(absPath, relPath string, idx *sourceIndex) {
	f, err := os.Open(absPath)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if m := funcServerRegex.FindStringSubmatch(line); m != nil {
			opID := m[1]
			ev := logger.EventName(opID)
			idx.handlersByEvent[ev] = SourceLocation{
				Symbol: opID,
				File:   relPath,
				Line:   lineNum,
			}
		}
		if m := clogGoRegex.FindStringSubmatch(line); m != nil {
			job := m[1]
			idx.jobsByName[job] = SourceLocation{
				Symbol: job,
				File:   relPath,
				Line:   lineNum,
			}
		}
		if m := clogGroupRegex.FindStringSubmatch(line); m != nil {
			grp := m[1]
			if strings.Contains(grp, ".") {
				if _, exists := idx.serviceGroups[grp]; !exists {
					idx.serviceGroups[grp] = SourceLocation{
						Symbol: grp,
						File:   relPath,
						Line:   lineNum,
					}
				}
			}
		}
	}
}

func stripQuery(rawPath string) string {
	if idx := strings.IndexByte(rawPath, '?'); idx >= 0 {
		return rawPath[:idx]
	}
	return rawPath
}

func extractURLPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return rawURL
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}

func isIDSegment(seg string) bool {
	if numericIDRegex.MatchString(seg) || uuidRegex.MatchString(seg) {
		return true
	}
	if alnumIDRegex.MatchString(seg) {
		// Ensure it contains at least one digit or uppercase letter so pure lowercase words aren't mistaken for IDs
		for _, r := range seg {
			if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') {
				return true
			}
		}
	}
	return false
}

// normalizeRoute replaces dynamic path IDs (Firestore IDs, Bungie membership/character IDs) with `:id`.
func normalizeRoute(rawPath string) string {
	pathOnly := stripQuery(rawPath)
	if pathOnly == "" {
		return ""
	}
	parts := strings.Split(pathOnly, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if isIDSegment(part) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

func normalizeErrorMessage(msg string) string {
	if msg == "" {
		return ""
	}
	// Collapse specific IDs in error strings so identical errors group together
	words := strings.Fields(msg)
	for i, w := range words {
		clean := strings.Trim(w, `"'(),.:;[]`)
		if isIDSegment(clean) {
			words[i] = "<id>"
		}
	}
	return strings.Join(words, " ")
}

func toInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return 0
	}
}

func writeJSONReport(path string, report MonitorReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeTextFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func renderMarkdownReport(report MonitorReport, repoRoot string) string {
	var b strings.Builder

	b.WriteString("# GCP Cloud Run Log Monitor Report\n\n")
	b.WriteString(fmt.Sprintf("- **Generated At**: `%s`\n", report.GeneratedAt))
	b.WriteString(fmt.Sprintf("- **GCP Project**: `%s` (`service=%s`)\n", report.ProjectID, report.ServiceName))
	b.WriteString(fmt.Sprintf("- **Lookback Window**: `%s` (since `%s`)\n", report.WindowSince, report.WindowStartUTC))
	b.WriteString(fmt.Sprintf("- **Slow Call Threshold**: `%s`\n", report.SlowThreshold))
	b.WriteString(fmt.Sprintf("- **Total Flagged Calls**: `%d` across `%d` issue group(s) (`%d` error group(s), `%d` slow group(s))\n",
		report.TotalFlaggedCalls, len(report.IssueGroups), report.TotalErrorGroups, report.TotalSlowGroups))
	if report.SuppressedScanner404 > 0 {
		b.WriteString(fmt.Sprintf("- **Suppressed Bot/Scanner 404s**: `%d` (`validate-request-failure` on non-existent routes)\n",
			report.SuppressedScanner404))
	}
	b.WriteString("\n")

	if len(report.IssueGroups) == 0 {
		b.WriteString("## Status: Healthy\n\nNo errors or slow calls exceeding threshold were found in this window.\n")
		return b.String()
	}

	b.WriteString("## Summary Table\n\n")
	b.WriteString("| # | Category | Event / Operation | Route / Job | Status | Count | Max Latency | P95 Latency | Error / Cause | Code Location |\n")
	b.WriteString("|---|----------|-------------------|-------------|--------|-------|-------------|-------------|---------------|---------------|\n")

	for i, g := range report.IssueGroups {
		route := fmt.Sprintf("`%s %s`", g.Method, g.RoutePattern)
		if g.Method == "BG_JOB" {
			route = fmt.Sprintf("`job: %s`", g.RoutePattern)
		} else if g.Method == "" {
			route = "`-`"
		}
		statusStr := "-"
		if g.Status > 0 {
			statusStr = fmt.Sprintf("`%d`", g.Status)
		}
		errSummary := g.ErrorMessage
		if errSummary == "" {
			errSummary = "*(latency threshold exceeded)*"
		} else if len(errSummary) > 70 {
			errSummary = errSummary[:67] + "..."
		}
		errSummary = strings.ReplaceAll(errSummary, "|", "\\|")

		locLink := "-"
		if g.HandlerLocation != nil {
			absFile := filepath.ToSlash(filepath.Join(repoRoot, g.HandlerLocation.File))
			locLink = fmt.Sprintf("[`%s`](file://%s#L%d)", g.HandlerLocation.Symbol, absFile, g.HandlerLocation.Line)
		}

		b.WriteString(fmt.Sprintf("| %d | **%s** | `%s` | %s | %s | %d | %.1fms | %.1fms | %s | %s |\n",
			i+1, g.Category, g.Event, route, statusStr, g.Count, g.MaxLatencyMs, g.P95LatencyMs, errSummary, locLink))
	}
	b.WriteString("\n---\n\n## Detailed Issue Breakdown\n\n")

	for i, g := range report.IssueGroups {
		b.WriteString(fmt.Sprintf("### %d. [%s] `%s` (%d occurrence(s))\n\n", i+1, g.Category, g.Event, g.Count))
		if g.Method == "BG_JOB" {
			b.WriteString(fmt.Sprintf("- **Background Job**: `%s`\n", g.JobName))
		} else if g.Method != "" {
			b.WriteString(fmt.Sprintf("- **Endpoint Pattern**: `%s %s` (HTTP Status: `%d`)\n", g.Method, g.RoutePattern, g.Status))
		}
		if g.ErrorMessage != "" {
			b.WriteString(fmt.Sprintf("- **Error Message**: `%s`\n", g.ErrorMessage))
		}
		b.WriteString(fmt.Sprintf("- **Latency (ms)**: min=`%.2fms`, avg=`%.2fms`, p95=`%.2fms`, max=`%.2fms`\n",
			g.MinLatencyMs, g.AvgLatencyMs, g.P95LatencyMs, g.MaxLatencyMs))
		b.WriteString(fmt.Sprintf("- **First / Last Seen**: `%s` -> `%s`\n", g.FirstSeen, g.LastSeen))

		if g.HandlerLocation != nil {
			absFile := filepath.ToSlash(filepath.Join(repoRoot, g.HandlerLocation.File))
			b.WriteString(fmt.Sprintf("- **Handler / Entrypoint**: [`%s` (%s:%d)](file://%s#L%d)\n",
				g.HandlerLocation.Symbol, g.HandlerLocation.File, g.HandlerLocation.Line, absFile, g.HandlerLocation.Line))
		}
		if len(g.ServiceLocations) > 0 {
			var links []string
			for _, sl := range g.ServiceLocations {
				absFile := filepath.ToSlash(filepath.Join(repoRoot, sl.File))
				links = append(links, fmt.Sprintf("[`%s` (%s:%d)](file://%s#L%d)", sl.Symbol, sl.File, sl.Line, absFile, sl.Line))
			}
			b.WriteString(fmt.Sprintf("- **Involved Service Methods (`clog` groups)**: %s\n", strings.Join(links, ", ")))
		}
		if len(g.Revisions) > 0 {
			b.WriteString(fmt.Sprintf("- **Cloud Run Revisions**: `%s`\n", strings.Join(g.Revisions, "`, `")))
		}
		b.WriteString("\n#### Sample Occurrences\n\n")
		for sIdx, s := range g.Samples {
			b.WriteString(fmt.Sprintf("##### Sample %d (`%s`, latency=`%s`, insertId=`%s`)\n\n",
				sIdx+1, s.Timestamp, s.Latency, s.InsertID))
			if s.Path != "" {
				b.WriteString(fmt.Sprintf("- **Full Path**: `%s %s`\n", s.Method, s.Path))
			}
			if len(s.Warnings) > 0 {
				b.WriteString(fmt.Sprintf("- **Warnings**: `%s`\n", strings.Join(s.Warnings, "; ")))
			}
			contextDump := map[string]any{}
			if len(s.RequestFields) > 0 {
				contextDump["request"] = s.RequestFields
			}
			if len(s.ActorFields) > 0 {
				contextDump["actor"] = s.ActorFields
			}
			if len(s.ServiceGroups) > 0 {
				contextDump["services"] = s.ServiceGroups
			}
			if len(s.ParentFields) > 0 {
				contextDump["parent"] = s.ParentFields
			}
			if len(contextDump) > 0 {
				rawCtx, _ := json.MarshalIndent(contextDump, "", "  ")
				b.WriteString("```json\n" + string(rawCtx) + "\n```\n")
			}
			if s.StackTrace != "" {
				b.WriteString("<details><summary>Stack Trace</summary>\n\n```text\n" + s.StackTrace + "\n```\n</details>\n")
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func printConsoleSummary(report MonitorReport, outMD, outJSON string) {
	fmt.Println()
	fmt.Printf("=== GCP Log Monitor Summary (%s, lookback=%s, slow>=%s) ===\n",
		report.ServiceName, report.WindowSince, report.SlowThreshold)
	fmt.Printf("Entries Evaluated: %d | Flagged Calls: %d | Issue Groups: %d (Errors: %d, Slow: %d) | Scanner 404s Suppressed: %d\n\n",
		report.TotalEntriesFetched, report.TotalFlaggedCalls, len(report.IssueGroups),
		report.TotalErrorGroups, report.TotalSlowGroups, report.SuppressedScanner404)

	if len(report.IssueGroups) == 0 {
		fmt.Println("No error or slow calls detected in the time window.")
	} else {
		fmt.Printf("%-4s %-11s %-38s %-6s %-6s %-10s %s\n",
			"#", "CATEGORY", "EVENT", "STATUS", "COUNT", "MAX_LAT", "LOCATION / ERROR")
		fmt.Println(strings.Repeat("-", 110))
		for i, g := range report.IssueGroups {
			loc := "-"
			if g.HandlerLocation != nil {
				loc = fmt.Sprintf("%s:%d", g.HandlerLocation.File, g.HandlerLocation.Line)
			}
			detail := loc
			if g.ErrorMessage != "" {
				detail = fmt.Sprintf("%s -> %s", loc, g.ErrorMessage)
			}
			if len(detail) > 48 {
				detail = detail[:45] + "..."
			}
			fmt.Printf("%-4d %-11s %-38s %-6d %-6d %-10s %s\n",
				i+1, g.Category, truncate(g.Event, 38), g.Status, g.Count,
				fmt.Sprintf("%.1fms", g.MaxLatencyMs), detail)
		}
	}

	fmt.Println()
	if outMD != "" {
		fmt.Printf("Markdown Report: %s\n", outMD)
	}
	if outJSON != "" {
		fmt.Printf("JSON Report:     %s\n", outJSON)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
