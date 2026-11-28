package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

type coreTier int

const (
	tierUnknown coreTier = iota
	tierPureLeaf
	tierPeerService
	tierWorkflow
)

var forbiddenCoreExternalPrefixes = []string{
	"oneTrick/api",
	"oneTrick/adapters",
	"oneTrick/clients",
	"oneTrick/services",
	"cloud.google.com/go/firestore",
	"github.com/gin-gonic/gin",
}

var pureLeafCorePackages = map[string]struct{}{
	"oneTrick/core/domain":    {},
	"oneTrick/core/gamemode":  {},
	"oneTrick/core/inventory": {},
}

var peerServiceCorePackages = map[string]struct{}{
	"oneTrick/core/activity":  {},
	"oneTrick/core/aggregate": {},
	"oneTrick/core/fireteam":  {},
	"oneTrick/core/session":   {},
	"oneTrick/core/snapshot":  {},
	"oneTrick/core/stats":     {},
	"oneTrick/core/telemetry": {},
	"oneTrick/core/user":      {},
}

var workflowCorePackages = map[string]struct{}{
	"oneTrick/core/auth":   {},
	"oneTrick/core/ticker": {},
}

type listedPackage struct {
	ImportPath   string   `json:"ImportPath"`
	ForTest      string   `json:"ForTest"`
	Imports      []string `json:"Imports"`
	Deps         []string `json:"Deps"`
	TestImports  []string `json:"TestImports"`
	XTestImports []string `json:"XTestImports"`
	Error        *struct {
		Err string `json:"Err"`
	} `json:"Error"`
	DepsErrors []struct {
		Err string `json:"Err"`
	} `json:"DepsErrors"`
}

func TestCoreHexagonalArchitectureBoundaries(t *testing.T) {
	cmd := exec.Command("go", "list", "-test", "-json", "./core/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -test -json ./core/... failed: %v\n%s", err, string(out))
	}

	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg listedPackage
		if err := dec.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("failed decoding go list JSON output: %v", err)
		}
		pkgs = append(pkgs, pkg)
	}

	seenBase := make(map[string]struct{}, len(pkgs))
	for _, pkg := range pkgs {
		if pkg.ForTest == "" && !strings.HasSuffix(pkg.ImportPath, ".test") {
			seenBase[pkg.ImportPath] = struct{}{}
		}
	}
	for _, required := range []string{
		"oneTrick/core/domain",
		"oneTrick/core/gamemode",
		"oneTrick/core/inventory",
		"oneTrick/core/activity",
		"oneTrick/core/fireteam",
		"oneTrick/core/telemetry",
	} {
		if _, ok := seenBase[required]; !ok {
			t.Fatalf("expected package %q in ./core/..., got %v", required, seenBase)
		}
	}

	if violations := evaluateCoreArchitecture(pkgs); len(violations) > 0 {
		t.Fatalf("core hexagonal architecture violations (%d):\n  - %s",
			len(violations), strings.Join(violations, "\n  - "))
	}
}

func evaluateCoreArchitecture(pkgs []listedPackage) []string {
	var violations []string
	seen := make(map[string]struct{})
	record := func(msg string) {
		if _, dup := seen[msg]; dup {
			return
		}
		seen[msg] = struct{}{}
		violations = append(violations, msg)
	}

	for _, pkg := range pkgs {
		if pkg.Error != nil && pkg.Error.Err != "" {
			record(fmt.Sprintf("%s: package error: %s", pkg.ImportPath, pkg.Error.Err))
		}
		for _, depErr := range pkg.DepsErrors {
			if depErr.Err != "" {
				record(fmt.Sprintf("%s: dependency error: %s", pkg.ImportPath, depErr.Err))
			}
		}

		allRefs := collectPackageRefs(pkg)
		for _, ref := range allRefs {
			cleanRef := stripTestVariantSuffix(ref)
			for _, forbidden := range forbiddenCoreExternalPrefixes {
				if matchesPackageOrSubpackage(cleanRef, forbidden) {
					record(fmt.Sprintf(
						"external boundary violation: %s references forbidden package %s",
						pkg.ImportPath, cleanRef,
					))
				}
			}
		}

		if strings.HasSuffix(pkg.ImportPath, ".test") {
			continue
		}

		self := pkg.ImportPath
		if pkg.ForTest != "" {
			self = pkg.ForTest
		}
		if !matchesPackageOrSubpackage(self, "oneTrick/core") {
			continue
		}

		selfTier := classifyCorePackage(self)
		if selfTier == tierUnknown {
			record(fmt.Sprintf(
				"unclassified core package %s: register in pureLeafCorePackages, peerServiceCorePackages, or workflowCorePackages in arch_test.go",
				self,
			))
			continue
		}

		for _, ref := range allRefs {
			cleanRef := stripTestVariantSuffix(ref)
			if cleanRef == self || !matchesPackageOrSubpackage(cleanRef, "oneTrick/core") {
				continue
			}

			targetTier := classifyCorePackage(cleanRef)
			if targetTier == tierUnknown {
				record(fmt.Sprintf(
					"unclassified core dependency %s referenced by %s",
					cleanRef, pkg.ImportPath,
				))
				continue
			}

			switch selfTier {
			case tierPureLeaf:
				if targetTier != tierPureLeaf {
					record(fmt.Sprintf(
						"leaf violation: pure value package %s must not import non-leaf core package %s",
						pkg.ImportPath, cleanRef,
					))
				}
			case tierPeerService:
				if targetTier != tierPureLeaf {
					record(fmt.Sprintf(
						"peer service violation: domain service %s must not import %s (orchestrate across domains via a Workflow)",
						pkg.ImportPath, cleanRef,
					))
				}
			case tierWorkflow:
				if targetTier == tierWorkflow {
					record(fmt.Sprintf(
						"workflow DAG violation: workflow %s must not import sibling workflow %s (max core depth is 2)",
						pkg.ImportPath, cleanRef,
					))
				}
			}
		}
	}

	slices.Sort(violations)
	return violations
}

func collectPackageRefs(pkg listedPackage) []string {
	total := len(pkg.Imports) + len(pkg.Deps) + len(pkg.TestImports) + len(pkg.XTestImports)
	refs := make([]string, 0, total)
	refs = append(refs, pkg.Imports...)
	refs = append(refs, pkg.Deps...)
	refs = append(refs, pkg.TestImports...)
	refs = append(refs, pkg.XTestImports...)
	return refs
}

func stripTestVariantSuffix(importPath string) string {
	if idx := strings.Index(importPath, " ["); idx != -1 {
		return importPath[:idx]
	}
	return importPath
}

func matchesPackageOrSubpackage(importPath, prefix string) bool {
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func classifyCorePackage(importPath string) coreTier {
	if _, ok := pureLeafCorePackages[importPath]; ok {
		return tierPureLeaf
	}
	if _, ok := peerServiceCorePackages[importPath]; ok {
		return tierPeerService
	}
	if _, ok := workflowCorePackages[importPath]; ok {
		return tierWorkflow
	}
	return tierUnknown
}

func TestEvaluateCoreArchitectureDetectsViolations(t *testing.T) {
	cases := []struct {
		name        string
		pkgs        []listedPackage
		wantSubstrs []string
	}{
		{
			name: "valid_dag_across_leaf_service_and_workflow",
			pkgs: []listedPackage{
				{
					ImportPath:  "oneTrick/core/inventory",
					Imports:     []string{"strconv"},
					XTestImports: []string{"oneTrick/core/inventory", "testing"},
				},
				{
					ImportPath: "oneTrick/core/domain",
					Imports:    []string{"oneTrick/core/inventory", "oneTrick/core/gamemode"},
				},
				{
					ImportPath: "oneTrick/core/snapshot",
					Imports:    []string{"oneTrick/core/domain", "oneTrick/core/inventory"},
				},
				{
					ImportPath: "oneTrick/core/ticker",
					Imports:    []string{"oneTrick/core/domain", "oneTrick/core/snapshot", "oneTrick/core/session"},
				},
			},
			wantSubstrs: nil,
		},
		{
			name: "external_boundary_in_deps_and_test_imports",
			pkgs: []listedPackage{
				{
					ImportPath:   "oneTrick/core/activity",
					Deps:         []string{"cloud.google.com/go/firestore"},
					TestImports:  []string{"oneTrick/api"},
					XTestImports: []string{"oneTrick/adapters/driven/firestore"},
				},
				{
					ImportPath: "oneTrick/core/activity.test",
					Deps:       []string{"oneTrick/services/user", "github.com/gin-gonic/gin", "oneTrick/clients/bungie"},
				},
			},
			wantSubstrs: []string{
				"cloud.google.com/go/firestore",
				"oneTrick/api",
				"oneTrick/adapters/driven/firestore",
				"oneTrick/services/user",
				"github.com/gin-gonic/gin",
				"oneTrick/clients/bungie",
			},
		},
		{
			name: "leaf_importing_service_is_rejected",
			pkgs: []listedPackage{
				{
					ImportPath: "oneTrick/core/domain",
					Imports:    []string{"oneTrick/core/snapshot"},
				},
			},
			wantSubstrs: []string{"leaf violation"},
		},
		{
			name: "peer_service_importing_peer_service_or_workflow_is_rejected",
			pkgs: []listedPackage{
				{
					ImportPath: "oneTrick/core/stats",
					Imports:    []string{"oneTrick/core/snapshot"},
				},
				{
					ImportPath:  "oneTrick/core/session",
					TestImports: []string{"oneTrick/core/ticker"},
				},
			},
			wantSubstrs: []string{
				"domain service oneTrick/core/stats must not import oneTrick/core/snapshot",
				"domain service oneTrick/core/session must not import oneTrick/core/ticker",
			},
		},
		{
			name: "workflow_importing_workflow_is_rejected",
			pkgs: []listedPackage{
				{
					ImportPath: "oneTrick/core/ticker",
					Imports:    []string{"oneTrick/core/auth"},
				},
			},
			wantSubstrs: []string{"workflow DAG violation"},
		},
		{
			name: "unclassified_core_package_is_rejected",
			pkgs: []listedPackage{
				{
					ImportPath: "oneTrick/core/unregistered",
					Imports:    []string{"strings"},
				},
			},
			wantSubstrs: []string{"unclassified core package oneTrick/core/unregistered"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateCoreArchitecture(tc.pkgs)
			if len(tc.wantSubstrs) == 0 {
				if len(got) != 0 {
					t.Fatalf("expected 0 violations, got %v", got)
				}
				return
			}
			joined := strings.Join(got, "\n")
			for _, sub := range tc.wantSubstrs {
				if !strings.Contains(joined, sub) {
					t.Errorf("expected violations to contain %q, got:\n%s", sub, joined)
				}
			}
		})
	}
}
