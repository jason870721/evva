package sysprompt

// Drift guard for the RP-19 curated table: every ToolName constant declared
// in pkg/tools/name.go must have a toolGuidelines entry, and every entry
// must correspond to a declared constant. Parsing name.go (rather than
// hand-maintaining a second list) makes adding a builtin tool without a
// usage guideline a CI failure — the same philosophy as the
// toolnames_link_test.go rename guard.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

const toolNameSourceFile = "../../../pkg/tools/name.go"

func declaredToolNames(t *testing.T) map[tools.ToolName]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, toolNameSourceFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", toolNameSourceFile, err)
	}
	declared := map[tools.ToolName]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "ToolName" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				declared[tools.ToolName(s)] = true
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no ToolName constants found in %s — parser drift?", toolNameSourceFile)
	}
	return declared
}

func TestToolGuidelines_CoverEveryBuiltin(t *testing.T) {
	declared := declaredToolNames(t)
	for n := range declared {
		if _, ok := toolGuidelines[n]; !ok {
			t.Errorf("builtin tool %q has no toolGuidelines entry — add a one-line usage guideline (and a toolGuideOrder slot) in disk_tools_guide.go", n)
		}
	}
	for n := range toolGuidelines {
		if !declared[n] {
			t.Errorf("toolGuidelines entry %q is not a declared ToolName constant in pkg/tools/name.go — stale after a tool rename or removal?", n)
		}
	}
}
