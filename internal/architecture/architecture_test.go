// Package architecture_test enforces the agent boundary independently of runtime behavior.
package architecture_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

const module = "github.com/flobilosaurus/agentnook"

var adapterNames = []string{"pi", "opencode", "claude"}

func repository(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture tests")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestPackageBoundaries(t *testing.T) {
	// Load from the module root, not this test directory. Never accept a vacuous pass
	// if arch-go's loader silently skips packages on a new Go/toolchain version.
	t.Chdir(repository(t))
	info := configuration.Load(module)
	if len(info.Packages) == 0 {
		t.Fatal("arch-go loaded no packages")
	}
	loaded := make(map[string]bool)
	for _, p := range info.Packages {
		loaded[p.Path] = true
	}
	allowed := map[string][]string{
		"agents":          {},
		"agents/registry": {"agents", "agents/pi", "agents/opencode", "agents/claude"},
		"agents/pi":       {"agents"}, "agents/opencode": {"agents"}, "agents/claude": {"agents"},
		"config": {"agents"}, "paths": {"agents", "config"},
		"vm": {"agents", "config"}, "images": {"agents", "config", "paths", "vm"},
		"telemetry": {"agents"}, "herdr": {"telemetry"}, "egress": {},
		"fleet": {"agents", "config", "paths", "telemetry"},
		"tui":   {"agents", "config", "images", "fleet", "egress"},
		"cli":   {"agents", "agents/registry", "config", "paths", "vm", "images", "telemetry", "herdr", "fleet", "tui", "egress"},
	}
	for _, p := range info.Packages {
		if strings.HasPrefix(p.Path, module+"/internal/") && (len(p.PackageData.GoFiles) > 0 || len(p.PackageData.CgoFiles) > 0) {
			if _, ok := allowed[strings.TrimPrefix(p.Path, module+"/internal/")]; !ok {
				t.Errorf("production package has no architecture rule: %s", p.Path)
			}
		}
	}
	var rules []*configuration.DependenciesRule
	for pkg, dependencies := range allowed {
		full := module + "/internal/" + pkg
		if !loaded[full] {
			t.Errorf("required package not loaded: %s", full)
		}
		rule := &configuration.DependenciesRule{Package: "^" + regexp.QuoteMeta(full) + "$"}
		if len(dependencies) == 0 {
			rule.ShouldNotDependsOn = &configuration.Dependencies{Internal: []string{"^" + regexp.QuoteMeta(module) + "/"}}
		} else {
			var imports []string
			for _, dependency := range dependencies {
				imports = append(imports, "^"+regexp.QuoteMeta(module+"/internal/"+dependency)+"$")
			}
			rule.ShouldOnlyDependsOn = &configuration.Dependencies{Internal: imports}
		}
		rules = append(rules, rule)
	}
	// Only dependency rules are used: arch-go evaluates different rule families in
	// parallel, so keeping one family also avoids shared-result writes under -race.
	result := archgo.CheckArchitecture(info, configuration.Config{DependenciesRules: rules})
	for _, rule := range result.DependenciesRuleResult.Results {
		if len(rule.Verifications) != 1 {
			t.Errorf("rule must inspect exactly one package, got %d: %s", len(rule.Verifications), rule.Rule.Package)
		}
		for _, verification := range rule.Verifications {
			if !verification.Passes {
				t.Errorf("%s: %v", verification.Package, verification.Details)
			}
		}
	}
	t.Run("arch-go rejects forbidden dependency", func(t *testing.T) {
		for _, p := range info.Packages {
			if p.Path != module+"/internal/telemetry" {
				continue
			}
			pkgCopy := *p
			dataCopy := *p.PackageData
			pkgCopy.PackageData = &dataCopy
			dataCopy.Imports = append(append([]string(nil), dataCopy.Imports...), module+"/internal/herdr")
			modified := info
			modified.Packages = append(modified.Packages[:0:0], &pkgCopy)
			check := archgo.CheckArchitecture(modified, configuration.Config{DependenciesRules: rules})
			if check.Pass {
				t.Fatal("arch-go accepted telemetry -> herdr; dependency rules are ineffective")
			}
			return
		}
		t.Fatal("telemetry package missing from negative control")
	})
}

func adapterDirectory(relative string) string {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) < 4 || parts[0] != "internal" || parts[1] != "agents" {
		return ""
	}
	for _, name := range adapterNames {
		if parts[2] == name {
			return name
		}
	}
	return ""
}

func sourceViolations(relative string, file *ast.File) []string {
	var violations []string
	adapter := adapterDirectory(relative)
	registry := filepath.ToSlash(filepath.Dir(relative)) == "internal/agents/registry"
	aliases := make(map[string]bool)
	for _, imp := range file.Imports {
		value, _ := strconv.Unquote(imp.Path.Value)
		if value == module+"/internal/agents" {
			alias := "agents"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = true
		}
		for _, name := range adapterNames {
			concrete := module + "/internal/agents/" + name
			if value == concrete || strings.HasPrefix(value, concrete+"/") {
				if !registry {
					violations = append(violations, "concrete adapter import outside registry: "+value)
				}
			}
		}
		if value == module+"/internal/agents/registry" && !strings.HasPrefix(relative, "internal/cli/") && !strings.HasPrefix(relative, "cmd/") {
			violations = append(violations, "registry dependency outside composition layer")
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if adapter != "" {
			if fn, ok := node.(*ast.FuncDecl); ok && fn.Name.Name == "init" {
				violations = append(violations, "adapter registration must not use init")
			}
			return true
		}
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if id, ok := selector.X.(*ast.Ident); ok && aliases[id.Name] {
				switch selector.Sel.Name {
				case "Pi", "OpenCode", "Claude":
					violations = append(violations, "concrete agent constant in shared code")
				}
			}
		}
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		for _, name := range adapterNames {
			if value == name {
				violations = append(violations, "concrete agent ID in shared code: "+name)
			}
		}
		for _, marker := range []string{"/.pi", "/.claude", "/opencode/", "@earendil-works/", "@opencode/", "@anthropic-ai/", "PI_CODING_AGENT_DIR", "CLAUDE_CONFIG_DIR", "OPENCODE_", "WORK_PI_", "claude.mjs", "claude-hook.mjs", "claude-settings.json", "status.ts"} {
			if strings.Contains(value, marker) {
				violations = append(violations, "agent-specific literal in shared code: "+marker)
			}
		}
		return true
	})
	return violations
}

func TestConcreteKnowledgeStaysInsideAdapters(t *testing.T) {
	root := repository(t)
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_test.mjs") {
			return nil
		}
		if strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".mjs") {
			if adapterDirectory(relative) == "" && filepath.ToSlash(relative) != "internal/agents/report.mjs" {
				t.Errorf("guest asset outside adapter: %s", relative)
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, violation := range sourceViolations(filepath.ToSlash(relative), file) {
			t.Errorf("%s: %s", relative, violation)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdaptersOwnTheirEmbeddedAssets(t *testing.T) {
	root := repository(t)
	assets := map[string][]string{"pi": {"status.ts"}, "opencode": {"plugin.mjs"}, "claude": {"supervisor.mjs", "hook.mjs"}}
	for name, required := range assets {
		dir := filepath.Join(root, "internal", "agents", name)
		files, err := parser.ParseDir(token.NewFileSet(), dir, func(info fs.FileInfo) bool { return !strings.HasSuffix(info.Name(), "_test.go") }, parser.ParseComments)
		if err != nil {
			t.Errorf("missing adapter %s: %v", name, err)
			continue
		}
		if len(files) != 1 {
			t.Errorf("adapter %s must contain one production package", name)
		}
		embedded := make(map[string]bool)
		for _, pkg := range files {
			for _, file := range pkg.Files {
				for _, group := range file.Comments {
					for _, comment := range group.List {
						if strings.HasPrefix(comment.Text, "//go:embed ") {
							for _, pattern := range strings.Fields(strings.TrimPrefix(comment.Text, "//go:embed ")) {
								embedded[pattern] = true
							}
						}
					}
				}
			}
		}
		for _, asset := range required {
			if _, err := os.Stat(filepath.Join(dir, asset)); err != nil {
				t.Errorf("%s must own %s: %v", name, asset, err)
			}
			if !embedded[asset] {
				t.Errorf("%s must embed its own %s", name, asset)
			}
		}
	}
}

func TestArchitectureGuardsDetectViolations(t *testing.T) {
	cases := []struct{ name, relative, source, want string }{
		{"adapter import", "internal/vm/leak.go", `package vm; import _ "` + module + `/internal/agents/pi"`, "concrete adapter import"},
		{"sibling adapter", "internal/agents/pi/leak.go", `package pi; import _ "` + module + `/internal/agents/claude"`, "concrete adapter import"},
		{"registry leak", "internal/fleet/leak.go", `package fleet; import _ "` + module + `/internal/agents/registry"`, "registry dependency"},
		{"constant alias", "internal/vm/leak.go", `package vm; import a "` + module + `/internal/agents"; var _ = a.Pi`, "concrete agent constant"},
		{"literal dispatch", "internal/vm/leak.go", `package vm; func f(agent string) { switch agent { case "pi": } }`, "concrete agent ID"},
		{"protocol knowledge", "internal/config/leak.go", `package config; const p = "/etc/opencode/plugins/agentnook"`, "agent-specific literal"},
		{"registry cannot hold behavior", "internal/agents/registry/leak.go", `package registry; func f(s string) { if s == "pi" {} }`, "concrete agent ID"},
		{"runtime command leak", "internal/telemetry/leak.go", `package telemetry; const script = "claude.mjs"`, "agent-specific literal"},
		{"side effect registration", "internal/agents/pi/leak.go", `package pi; func init() {}`, "must not use init"},
		{"generic contract", "internal/agents/agent.go", `package agents; type Agent interface { ID() string }`, ""},
		{"adapter details", "internal/agents/pi/agent.go", `package pi; const name = "pi"`, ""},
		{"registry wiring", "internal/agents/registry/registry.go", `package registry; import _ "` + module + `/internal/agents/pi"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tc.relative, tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := sourceViolations(tc.relative, file)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatal(got)
				}
				return
			}
			if !strings.Contains(fmt.Sprint(got), tc.want) {
				t.Fatalf("guard missed %q: %v", tc.want, got)
			}
		})
	}
}
