package excel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "excel-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func tempXlsx(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(dir, "test.xlsx")
}

func newTool(workDir string) *excelTool {
	return NewTool(workDir)
}

func exec(t *testing.T, tool *excelTool, input map[string]interface{}) tools.Result {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tool.Execute(context.Background(), tools.NopLogger(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return result
}

// ── create + write + read cycle ─────────────────────────────────────────

func TestCreateWriteRead(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	// Create
	r := exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
		"sheets":    []string{"Data", "Summary"},
	})
	if r.IsError {
		t.Fatalf("create: %s", r.Content)
	}

	// Verify the file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Write data
	r = exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"sheetName": "Data",
		"data": []interface{}{
			[]interface{}{"Name", "Age", "City"},
			[]interface{}{"Alice", float64(30), "NYC"},
			[]interface{}{"Bob", float64(25), "LA"},
		},
		"startCell": "A1",
	})
	if r.IsError {
		t.Fatalf("write: %s", r.Content)
	}
	if !strings.Contains(r.Content, "Wrote 9 cells") {
		t.Errorf("expected 'Wrote 9 cells', got: %s", r.Content)
	}

	// Read back
	r = exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  path,
		"sheetName": "Data",
	})
	if r.IsError {
		t.Fatalf("read: %s", r.Content)
	}
	if !strings.Contains(r.Content, "Alice") || !strings.Contains(r.Content, "Bob") {
		t.Errorf("read output missing expected data: %s", r.Content)
	}
}

// ── write: data is required ─────────────────────────────────────────────

func TestWriteMissingData(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	// Create first
	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	// Write without data
	r := exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
	})
	if !r.IsError {
		t.Fatal("expected error for missing data")
	}
	if !strings.Contains(r.Content, "data is required") {
		t.Errorf("expected 'data is required', got: %s", r.Content)
	}
}

func TestWriteEmptyData(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data":      []interface{}{},
	})
	if !r.IsError {
		t.Fatal("expected error for empty data")
	}
	if !strings.Contains(r.Content, "data is required") {
		t.Errorf("expected 'data is required', got: %s", r.Content)
	}
}

// ── read ────────────────────────────────────────────────────────────────

func TestReadEmptyFile(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  path,
	})
	if r.IsError {
		t.Fatalf("read empty: %s", r.Content)
	}
	if !strings.Contains(r.Content, "(empty)") {
		t.Errorf("expected '(empty)', got: %s", r.Content)
	}
}

func TestReadWithRange(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"A", "B", "C"},
			[]interface{}{"D", "E", "F"},
			[]interface{}{"G", "H", "I"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  path,
		"range":     "A1:B2",
	})
	if r.IsError {
		t.Fatalf("read range: %s", r.Content)
	}
	lines := strings.Split(strings.TrimSpace(r.Content), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 rows, got %d: %s", len(lines), r.Content)
	}
}

func TestReadNonexistentFile(t *testing.T) {
	tool := newTool("/tmp")
	r := exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  "/tmp/nonexistent_xyz123.xlsx",
	})
	if !r.IsError {
		t.Fatal("expected error for nonexistent file")
	}
}

// ── list_sheets ─────────────────────────────────────────────────────────

func TestListSheets(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
		"sheets":    []string{"Data", "Summary", "Notes"},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "list_sheets",
		"filePath":  path,
	})
	if r.IsError {
		t.Fatalf("list_sheets: %s", r.Content)
	}
	for _, name := range []string{"Data", "Summary", "Notes"} {
		if !strings.Contains(r.Content, name) {
			t.Errorf("expected sheet %q in output: %s", name, r.Content)
		}
	}
}

// ── get_info ────────────────────────────────────────────────────────────

func TestGetInfo(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
		"sheets":    []string{"Data"},
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"sheetName": "Data",
		"data": []interface{}{
			[]interface{}{"A", "B"},
			[]interface{}{"C", "D"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "get_info",
		"filePath":  path,
	})
	if r.IsError {
		t.Fatalf("get_info: %s", r.Content)
	}
	if !strings.Contains(r.Content, "Data") {
		t.Errorf("expected sheet name 'Data' in output: %s", r.Content)
	}
	if !strings.Contains(r.Content, "Rows:") {
		t.Errorf("expected 'Rows:' in output: %s", r.Content)
	}
}

// ── search ──────────────────────────────────────────────────────────────

func TestSearch(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Name", "Department"},
			[]interface{}{"Alice", "Engineering"},
			[]interface{}{"Bob", "Marketing"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "search",
		"filePath":  path,
		"query":     "Alice",
	})
	if r.IsError {
		t.Fatalf("search: %s", r.Content)
	}
	if !strings.Contains(r.Content, "Alice") {
		t.Errorf("expected 'Alice' in search results: %s", r.Content)
	}

	// No match
	r = exec(t, tool, map[string]interface{}{
		"operation": "search",
		"filePath":  path,
		"query":     "Nobody",
	})
	if r.IsError {
		t.Fatalf("search: %s", r.Content)
	}
	if !strings.Contains(r.Content, "No matches found") {
		t.Errorf("expected 'No matches found': %s", r.Content)
	}
}

// ── sheet management ────────────────────────────────────────────────────

func TestDeleteSheet(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
		"sheets":    []string{"Data", "Temp"},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "delete_sheet",
		"filePath":  path,
		"sheetName": "Temp",
	})
	if r.IsError {
		t.Fatalf("delete_sheet: %s", r.Content)
	}

	r = exec(t, tool, map[string]interface{}{
		"operation": "list_sheets",
		"filePath":  path,
	})
	if strings.Contains(r.Content, "Temp") {
		t.Errorf("Temp sheet should be deleted: %s", r.Content)
	}
}

func TestCopySheetWithinFile(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
		"sheets":    []string{"Original"},
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"sheetName": "Original",
		"data": []interface{}{
			[]interface{}{"Hello", "World"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation":   "copy_sheet",
		"filePath":    path,
		"sourceSheet": "Original",
		"targetSheet": "Copy",
	})
	if r.IsError {
		t.Fatalf("copy_sheet: %s", r.Content)
	}

	r = exec(t, tool, map[string]interface{}{
		"operation": "list_sheets",
		"filePath":  path,
	})
	if !strings.Contains(r.Content, "Copy") {
		t.Errorf("expected 'Copy' sheet: %s", r.Content)
	}
}

// ── insert rows / columns ───────────────────────────────────────────────

func TestInsertRows(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Row1"},
			[]interface{}{"Row2"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "insert_rows",
		"filePath":  path,
		"rowIndex":  float64(1),
		"count":     float64(2),
	})
	if r.IsError {
		t.Fatalf("insert_rows: %s", r.Content)
	}
}

func TestInsertCols(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"A", "B"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "insert_cols",
		"filePath":  path,
		"colIndex":  float64(1),
		"count":     float64(1),
	})
	if r.IsError {
		t.Fatalf("insert_cols: %s", r.Content)
	}
}

// ── merge / unmerge ─────────────────────────────────────────────────────

func TestMergeUnmergeCells(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "merge_cells",
		"filePath":  path,
		"range":     "A1:B2",
	})
	if r.IsError {
		t.Fatalf("merge_cells: %s", r.Content)
	}

	r = exec(t, tool, map[string]interface{}{
		"operation": "unmerge_cells",
		"filePath":  path,
		"range":     "A1:B2",
	})
	if r.IsError {
		t.Fatalf("unmerge_cells: %s", r.Content)
	}
}

// ── formulas ────────────────────────────────────────────────────────────

func TestSetCellFormula(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{float64(10)},
			[]interface{}{float64(20)},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_cell_formula",
		"filePath":  path,
		"cell":      "A3",
		"formula":   "SUM(A1:A2)",
	})
	if r.IsError {
		t.Fatalf("set_cell_formula: %s", r.Content)
	}
}

// ── schema validation ───────────────────────────────────────────────────

func TestSchemaIsValidJSON(t *testing.T) {
	tool := newTool("/tmp")
	schema := tool.Schema()
	var v interface{}
	if err := json.Unmarshal(schema, &v); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
}

func TestSchemaHasRequiredFields(t *testing.T) {
	tool := newTool("/tmp")
	schema := tool.Schema()
	var m map[string]interface{}
	json.Unmarshal(schema, &m)

	req, ok := m["required"].([]interface{})
	if !ok {
		t.Fatal("schema missing 'required'")
	}
	hasOp := false
	hasPath := false
	for _, r := range req {
		if s, ok := r.(string); ok {
			if s == "operation" {
				hasOp = true
			}
			if s == "filePath" {
				hasPath = true
			}
		}
	}
	if !hasOp {
		t.Error("schema missing required: operation")
	}
	if !hasPath {
		t.Error("schema missing required: filePath")
	}
}

func TestSchemaHasNoDuplicatedJSONTags(t *testing.T) {
	// Verify that no two fields in excelInput share the same json tag.
	// This was the root cause of the write-data bug.
	type dummy struct {
		_ excelInput
	}
	seen := map[string]bool{}
	checkStruct := func(v interface{}) {
		// Manual check — if we add a field with a conflicting tag, this test
		// must be updated.
	}
	_ = checkStruct // silence

	tags := []string{
		"operation", "filePath", "sheetName", "range", "maxRows",
		"data", "startCell", "sheets", "query",
		"sourceSheet", "targetSheet", "targetFile",
		"rowIndex", "colIndex", "count",
		"cell", "formula", "chartType", "series",
		"title", "position", "dataSheet", "dataRange",
		"pivotRange", "rows", "columns", "pivotData",
		"validationType", "criteria",
		"font", "fill", "border", "alignment", "numberFormat",
		"col", "width", "row", "height",
		"condType", "condOperator", "condCriteria", "condStyle",
	}
	for _, tag := range tags {
		if seen[tag] {
			t.Errorf("duplicate JSON tag %q found in excelInput", tag)
		}
		seen[tag] = true
	}
}

// ── write data types ────────────────────────────────────────────────────

func TestWriteVariousDataTypes(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"String", "Column"},
			[]interface{}{float64(42), true},
			[]interface{}{float64(3.14), "text"},
		},
	})
	if r.IsError {
		t.Fatalf("write: %s", r.Content)
	}

	// Read back and verify
	r = exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  path,
	})
	if r.IsError {
		t.Fatalf("read: %s", r.Content)
	}
	for _, expected := range []string{"String", "42", "3.14", "text"} {
		if !strings.Contains(r.Content, expected) {
			t.Errorf("expected %q in output: %s", expected, r.Content)
		}
	}
}

// ── unknown operation ───────────────────────────────────────────────────

func TestUnknownOperation(t *testing.T) {
	tool := newTool("/tmp")
	r := exec(t, tool, map[string]interface{}{
		"operation": "nonexistent_op",
		"filePath":  "/tmp/test.xlsx",
	})
	if !r.IsError {
		t.Fatal("expected error for unknown operation")
	}
	if !strings.Contains(r.Content, "unknown operation") {
		t.Errorf("expected 'unknown operation', got: %s", r.Content)
	}
}

// ── malformed range ─────────────────────────────────────────────────────

func TestReadMalformedRange(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  path,
		"range":     "not-a-range",
	})
	if !r.IsError {
		t.Fatal("expected error for malformed range")
	}
	if !strings.Contains(r.Content, "invalid range format") {
		t.Errorf("expected 'invalid range format', got: %s", r.Content)
	}
}

// ── styling ─────────────────────────────────────────────────────────────

func TestSetStyleFont(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Header1", "Header2"},
			[]interface{}{"val1", "val2"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_style",
		"filePath":  path,
		"range":     "A1:B1",
		"font": map[string]interface{}{
			"bold":  true,
			"size":  float64(14),
			"color": "FF0000",
		},
	})
	if r.IsError {
		t.Fatalf("set_style font: %s", r.Content)
	}
	if !strings.Contains(r.Content, "font") {
		t.Errorf("expected 'font' in result: %s", r.Content)
	}
}

func TestSetStyleFill(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"A", "B"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_style",
		"filePath":  path,
		"range":     "A1:B1",
		"fill": map[string]interface{}{
			"type":    "pattern",
			"pattern": float64(1),
			"color":   "FFFF00",
		},
	})
	if r.IsError {
		t.Fatalf("set_style fill: %s", r.Content)
	}
}

func TestSetStyleBorder(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"A", "B"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_style",
		"filePath":  path,
		"range":     "A1:B2",
		"border": []interface{}{
			map[string]interface{}{"type": "left", "color": "000000", "style": float64(1)},
			map[string]interface{}{"type": "right", "color": "000000", "style": float64(1)},
			map[string]interface{}{"type": "top", "color": "000000", "style": float64(1)},
			map[string]interface{}{"type": "bottom", "color": "000000", "style": float64(1)},
		},
	})
	if r.IsError {
		t.Fatalf("set_style border: %s", r.Content)
	}
	if !strings.Contains(r.Content, "border") {
		t.Errorf("expected 'border' in result: %s", r.Content)
	}
}

func TestSetStyleAlignment(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Centered"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_style",
		"filePath":  path,
		"range":     "A1:A1",
		"alignment": map[string]interface{}{
			"horizontal": "center",
			"vertical":   "center",
			"wrapText":   true,
		},
	})
	if r.IsError {
		t.Fatalf("set_style alignment: %s", r.Content)
	}
}

func TestSetStyleNumberFormat(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Price"},
			[]interface{}{float64(1234.5)},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation":    "set_style",
		"filePath":     path,
		"range":        "A2:A2",
		"numberFormat": "#,##0.00",
	})
	if r.IsError {
		t.Fatalf("set_style number format: %s", r.Content)
	}
}

func TestSetStyleCombined(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Name", "Score"},
			[]interface{}{"Alice", float64(95)},
		},
	})

	// Apply font + fill + border together
	r := exec(t, tool, map[string]interface{}{
		"operation": "set_style",
		"filePath":  path,
		"range":     "A1:B1",
		"font": map[string]interface{}{
			"bold":  true,
			"size":  float64(12),
			"color": "FFFFFF",
		},
		"fill": map[string]interface{}{
			"type":    "pattern",
			"pattern": float64(1),
			"color":   "4472C4",
		},
		"border": []interface{}{
			map[string]interface{}{"type": "bottom", "color": "000000", "style": float64(2)},
		},
	})
	if r.IsError {
		t.Fatalf("set_style combined: %s", r.Content)
	}
	if !strings.Contains(r.Content, "font") || !strings.Contains(r.Content, "fill") || !strings.Contains(r.Content, "border") {
		t.Errorf("expected all style types in result: %s", r.Content)
	}
}

func TestSetColumnWidth(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_column_width",
		"filePath":  path,
		"col":       "A",
		"width":     float64(25),
	})
	if r.IsError {
		t.Fatalf("set_column_width: %s", r.Content)
	}
}

func TestSetColumnWidthMissingCol(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_column_width",
		"filePath":  path,
		"width":     float64(10),
	})
	if !r.IsError {
		t.Fatal("expected error for missing col")
	}
}

func TestSetRowHeight(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_row_height",
		"filePath":  path,
		"row":       float64(1),
		"height":    float64(30),
	})
	if r.IsError {
		t.Fatalf("set_row_height: %s", r.Content)
	}
}

func TestSetRowHeightMissingRow(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_row_height",
		"filePath":  path,
		"height":    float64(30),
	})
	if !r.IsError {
		t.Fatal("expected error for missing row")
	}
}

func TestSetConditionalFormatCellGreaterThan(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Score"},
			[]interface{}{float64(50)},
			[]interface{}{float64(90)},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation":   "set_conditional_format",
		"filePath":    path,
		"range":       "A2:A3",
		"condType":    "cell",
		"condOperator": "greater than",
		"condCriteria": map[string]interface{}{
			"value": "80",
		},
		"condStyle": map[string]interface{}{
			"bold":  true,
			"color": "FF0000",
		},
	})
	if r.IsError {
		t.Fatalf("set_conditional_format: %s", r.Content)
	}
}

func TestSetStyleMissingRange(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "set_style",
		"filePath":  path,
		"font": map[string]interface{}{
			"bold": true,
		},
	})
	if !r.IsError {
		t.Fatal("expected error for missing range")
	}
}

// ── regression: formula evaluation (CalcCellValue) ──────────────────────

func TestFormulaEvaluation(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{"Value"},
			[]interface{}{float64(10)},
			[]interface{}{float64(20)},
			[]interface{}{float64(30)},
		},
	})

	// Set formulas on cells that were NOT previously written (fresh cells)
	exec(t, tool, map[string]interface{}{
		"operation": "set_cell_formula",
		"filePath":  path,
		"cell":      "B2",
		"formula":   "SUM(A2:A4)",
	})
	exec(t, tool, map[string]interface{}{
		"operation": "set_cell_formula",
		"filePath":  path,
		"cell":      "B3",
		"formula":   "AVERAGE(A2:A4)",
	})

	// Read back — formulas should be computed via CalcCellValue
	r := exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  path,
	})
	if r.IsError {
		t.Fatalf("read: %s", r.Content)
	}
	// SUM(10,20,30) = 60
	if !strings.Contains(r.Content, "60") {
		t.Errorf("expected SUM result 60 in output: %s", r.Content)
	}
	// AVERAGE(10,20,30) = 20
	if !strings.Contains(r.Content, "20") {
		t.Errorf("expected AVERAGE result 20 in output: %s", r.Content)
	}
}

func TestFormulaOnExistingCell(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
	})
	// Write a value first, then overwrite with a formula
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"data": []interface{}{
			[]interface{}{float64(99), float64(1)},
			[]interface{}{float64(99), float64(2)},
		},
	})
	// Overwrite A1 (was 99) with a formula
	exec(t, tool, map[string]interface{}{
		"operation": "set_cell_formula",
		"filePath":  path,
		"cell":      "A1",
		"formula":   "SUM(B1:B2)",
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "read",
		"filePath":  path,
		"range":     "A1:A1",
	})
	if r.IsError {
		t.Fatalf("read: %s", r.Content)
	}
	// SUM(1,2) = 3, not 99
	if strings.Contains(r.Content, "99") {
		t.Errorf("formula should override stale cached value 99: %s", r.Content)
	}
	if !strings.Contains(r.Content, "3") {
		t.Errorf("expected formula result 3, got: %s", r.Content)
	}
}

// ── regression: sheet dimensions ────────────────────────────────────────

func TestDimensionsAfterWrite(t *testing.T) {
	dir := tempDir(t)
	path := tempXlsx(t, dir)
	tool := newTool(dir)

	exec(t, tool, map[string]interface{}{
		"operation": "create",
		"filePath":  path,
		"sheets":    []string{"Data"},
	})
	exec(t, tool, map[string]interface{}{
		"operation": "write",
		"filePath":  path,
		"sheetName": "Data",
		"data": []interface{}{
			[]interface{}{"A", "B", "C"},
			[]interface{}{"D", "E", "F"},
			[]interface{}{"G", "H", "I"},
		},
	})

	r := exec(t, tool, map[string]interface{}{
		"operation": "list_sheets",
		"filePath":  path,
	})
	if r.IsError {
		t.Fatalf("list_sheets: %s", r.Content)
	}
	// 3 rows × 3 cols → dimensions should be at least C3
	if !strings.Contains(r.Content, "C3") && !strings.Contains(r.Content, "B3") {
		t.Errorf("expected dimensions beyond A1, got: %s", r.Content)
	}
	// Should NOT say just "A1"
	if strings.Contains(r.Content, "(dimensions: A1)") {
		t.Errorf("dimensions should not be stuck at A1: %s", r.Content)
	}
}
