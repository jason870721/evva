package excel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/xuri/excelize/v2"
)

type excelTool struct {
	workDir string
}

// NewTool creates an excelTool bound to the given working directory.
func NewTool(workDir string) *excelTool {
	return &excelTool{workDir: workDir}
}

func (t *excelTool) Name() string { return string(tools.EXCEL) }

func (t *excelTool) Description() string {
	return "Read, write, create, and manipulate Excel (.xlsx) spreadsheets. " +
		"Supports reading/writing cell values, listing sheets, searching, " +
		"inserting rows/columns, merging cells, formulas, charts, pivot tables, " +
		"and data validation. Use for: inspecting spreadsheet contents, " +
		"programmatically building reports, modifying existing workbooks, " +
		"or creating new ones from scratch."
}

func (t *excelTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "required": ["operation", "filePath"],
  "properties": {
    "operation": {
      "type": "string",
      "enum": ["read", "write", "create", "list_sheets", "get_info", "search", "copy_sheet", "delete_sheet", "insert_rows", "insert_cols", "merge_cells", "unmerge_cells", "set_cell_formula", "add_chart", "add_pivot_table", "add_data_validation", "set_style", "set_column_width", "set_row_height", "set_conditional_format"],
      "description": "Operation to perform on the spreadsheet."
    },
    "filePath": {
      "type": "string",
      "description": "Absolute path to the .xlsx file."
    },
    "sheetName": {
      "type": "string",
      "description": "Sheet name to operate on. Defaults to the first sheet when omitted."
    },
    "range": {
      "type": "string",
      "description": "Cell range in Excel notation (e.g. \"A1:D10\")."
    },
    "maxRows": {
      "type": "integer",
      "description": "Maximum number of data rows to return. Default 100."
    },
    "data": {
      "type": "array",
      "items": {"type": "array", "items": {}},
      "description": "2D array of values to write, e.g. [[\"Name\",\"Age\"],[\"Alice\",30],[\"Bob\",25]]."
    },
    "startCell": {
      "type": "string",
      "description": "Top-left cell to start writing from (e.g. \"A1\"). Default \"A1\"."
    },
    "sheets": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Sheet names to create. Default [\"Sheet1\"]."
    },
    "query": {
      "type": "string",
      "description": "Text to search for in cell values."
    },
    "sourceSheet": {
      "type": "string",
      "description": "Name of the sheet to copy from."
    },
    "targetSheet": {
      "type": "string",
      "description": "Name for the destination sheet."
    },
    "targetFile": {
      "type": "string",
      "description": "Target file path (for cross-workbook copy)."
    },
    "rowIndex": {
      "type": "integer",
      "description": "Row index to insert at (1-indexed)."
    },
    "colIndex": {
      "type": "integer",
      "description": "Column index to insert at (1-indexed)."
    },
    "count": {
      "type": "integer",
      "description": "Number of rows or columns to insert. Default 1."
    },
    "cell": {
      "type": "string",
      "description": "Cell reference (e.g. \"C1\")."
    },
    "formula": {
      "type": "string",
      "description": "Excel formula (e.g. \"SUM(A1:A10)\")."
    },
    "chartType": {
      "type": "string",
      "enum": ["bar", "line", "pie", "scatter", "area", "doughnut", "radar"],
      "description": "Chart type."
    },
    "series": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "description": "Series name."},
          "categories": {"type": "string", "description": "Category range (e.g. \"Sheet1!$A$1:$A$5\")."},
          "values": {"type": "string", "description": "Value range (e.g. \"Sheet1!$B$1:$B$5\")."}
        }
      },
      "description": "Chart data series."
    },
    "title": {
      "type": "string",
      "description": "Chart title."
    },
    "position": {
      "type": "string",
      "description": "Chart position cell (e.g. \"H1\"). Default \"H1\"."
    },
    "dataSheet": {
      "type": "string",
      "description": "Sheet containing the source data for a pivot table."
    },
    "dataRange": {
      "type": "string",
      "description": "Source data range for a pivot table (e.g. \"A1:D100\")."
    },
    "pivotRange": {
      "type": "string",
      "description": "Top-left cell for the pivot table (e.g. \"F1\")."
    },
    "rows": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Field names to use as pivot table row labels."
    },
    "columns": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Field names to use as pivot table column labels."
    },
    "pivotData": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Field names to aggregate in pivot table, with optional summary function (e.g. \"Amount:Sum\")."
    },
    "validationType": {
      "type": "string",
      "enum": ["list", "whole", "decimal", "date", "time", "textLength", "custom"],
      "description": "Data validation type."
    },
    "criteria": {
      "type": "object",
      "description": "Validation criteria. For list: {\"values\":[\"a\",\"b\",\"c\"]}. For numeric: {\"operator\":\"greaterThan\",\"value1\":0,\"value2\":100}."
    },
    "font": {
      "type": "object",
      "properties": {
        "bold": {"type": "boolean", "description": "Bold text."},
        "italic": {"type": "boolean", "description": "Italic text."},
        "underline": {"type": "string", "enum": ["single", "double"], "description": "Underline style."},
        "family": {"type": "string", "description": "Font family name (e.g. \"Arial\", \"Times New Roman\")."},
        "size": {"type": "number", "description": "Font size in points (e.g. 12)."},
        "strike": {"type": "boolean", "description": "Strikethrough text."},
        "color": {"type": "string", "description": "Font color in RRGGBB hex (e.g. \"FF0000\" for red)."}
      },
      "description": "Font styling to apply."
    },
    "fill": {
      "type": "object",
      "properties": {
        "type": {"type": "string", "enum": ["pattern", "gradient"], "description": "Fill type. Default \"pattern\"."},
        "pattern": {"type": "integer", "description": "Pattern index (1=solid). Default 1."},
        "color": {"type": "string", "description": "Fill color in RRGGBB hex (e.g. \"FFFF00\" for yellow)."}
      },
      "description": "Cell background fill."
    },
    "border": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "type": {"type": "string", "enum": ["left", "right", "top", "bottom", "diagonalUp", "diagonalDown"], "description": "Border edge."},
          "color": {"type": "string", "description": "Border color in RRGGBB hex."},
          "style": {"type": "integer", "description": "Border style: 0=none, 1=thin, 2=medium, 3=dashed, 4=dotted, 5=thick, 6=double, 7=hair, 8=mediumDashed, 9=dashDot, 10=mediumDashDot, 11=dashDotDot, 12=mediumDashDotDot, 13=slantedDashDot."}
        }
      },
      "description": "Cell borders. Add one object per edge."
    },
    "alignment": {
      "type": "object",
      "properties": {
        "horizontal": {"type": "string", "enum": ["left", "center", "right", "fill", "justify", "centerContinuous", "distributed"], "description": "Horizontal alignment."},
        "vertical": {"type": "string", "enum": ["top", "center", "bottom", "justify", "distributed"], "description": "Vertical alignment."},
        "wrapText": {"type": "boolean", "description": "Enable text wrapping."},
        "textRotation": {"type": "integer", "description": "Text rotation angle (0-180)."},
        "indent": {"type": "integer", "description": "Indent level."},
        "shrinkToFit": {"type": "boolean", "description": "Shrink text to fit cell width."}
      },
      "description": "Cell text alignment."
    },
    "numberFormat": {
      "type": "string",
      "description": "Excel number format string (e.g. \"0.00\", \"#,##0\", \"yyyy-mm-dd\", \"0.00%\")."
    },
    "col": {
      "type": "string",
      "description": "Column identifier for width setting (e.g. \"A\", \"B\", \"A:C\")."
    },
    "width": {
      "type": "number",
      "description": "Column width in character units (e.g. 20)."
    },
    "row": {
      "type": "integer",
      "description": "Row number for height setting (1-indexed)."
    },
    "height": {
      "type": "number",
      "description": "Row height in points (e.g. 30)."
    },
    "condType": {
      "type": "string",
      "enum": ["cell", "top", "bottom", "average", "duplicate", "unique", "text", "blanks", "no_blanks", "errors", "no_errors", "2_color_scale", "3_color_scale", "data_bar", "formula", "icon_set"],
      "description": "Conditional format type."
    },
    "condOperator": {
      "type": "string",
      "description": "Operator/criteria for conditional format (e.g. \"greater than\", \"between\", \">=\", \"=\", \"containsText\", \"begins with\"). See excelize criteriaType map for full list."
    },
    "condCriteria": {
      "type": "object",
      "properties": {
        "value": {"type": "string", "description": "Criteria value (e.g. \"10\")."},
        "value2": {"type": "string", "description": "Second value for between/notBetween operators."}
      },
      "description": "Criteria values for the conditional format."
    },
    "condStyle": {
      "type": "object",
      "properties": {
        "bold": {"type": "boolean"},
        "italic": {"type": "boolean"},
        "color": {"type": "string", "description": "Font color in RRGGBB hex."},
        "size": {"type": "number"},
        "family": {"type": "string"}
      },
      "description": "Font style to apply when condition is met."
    }
  }
}`)
}

type excelInput struct {
	Operation      string            `json:"operation"`
	FilePath       string            `json:"filePath"`
	SheetName      string            `json:"sheetName"`
	Range          string            `json:"range"`
	MaxRows        int               `json:"maxRows"`
	Data           [][]interface{}   `json:"data"`
	StartCell      string            `json:"startCell"`
	Sheets         []string          `json:"sheets"`
	Query          string            `json:"query"`
	SourceSheet    string            `json:"sourceSheet"`
	TargetSheet    string            `json:"targetSheet"`
	TargetFile     string            `json:"targetFile"`
	RowIndex       int               `json:"rowIndex"`
	ColIndex       int               `json:"colIndex"`
	Count          int               `json:"count"`
	Cell           string            `json:"cell"`
	Formula        string            `json:"formula"`
	ChartType      string            `json:"chartType"`
	Series         []chartSeriesSpec `json:"series"`
	Title          string            `json:"title"`
	Position       string            `json:"position"`
	DataSheet      string            `json:"dataSheet"`
	DataRange      string            `json:"dataRange"`
	PivotRange     string            `json:"pivotRange"`
	Rows           []string          `json:"rows"`
	Columns        []string          `json:"columns"`
	PivotData      []string          `json:"pivotData"`
	ValidationType string            `json:"validationType"`
	Criteria       json.RawMessage   `json:"criteria"`
	Font           *fontSpec         `json:"font"`
	Fill           *fillSpec         `json:"fill"`
	Border         []borderSpec      `json:"border"`
	Alignment      *alignmentSpec    `json:"alignment"`
	NumberFormat   string            `json:"numberFormat"`
	Col            string            `json:"col"`
	Width          float64           `json:"width"`
	Row            int               `json:"row"`
	Height         float64           `json:"height"`
	CondType       string            `json:"condType"`
	CondOperator   string            `json:"condOperator"`
	CondCriteria   json.RawMessage   `json:"condCriteria"`
	CondStyle      *fontSpec         `json:"condStyle"`
}

type chartSeriesSpec struct {
	Name       string `json:"name"`
	Categories string `json:"categories"`
	Values     string `json:"values"`
}

type fontSpec struct {
	Bold      bool    `json:"bold"`
	Italic    bool    `json:"italic"`
	Underline string  `json:"underline"`
	Family    string  `json:"family"`
	Size      float64 `json:"size"`
	Strike    bool    `json:"strike"`
	Color     string  `json:"color"`
}

type fillSpec struct {
	Type    string `json:"type"`
	Pattern int    `json:"pattern"`
	Color   string `json:"color"`
}

type borderSpec struct {
	Type  string `json:"type"`
	Color string `json:"color"`
	Style int    `json:"style"`
}

type alignmentSpec struct {
	Horizontal  string `json:"horizontal"`
	Vertical    string `json:"vertical"`
	WrapText    bool   `json:"wrapText"`
	TextRotation int   `json:"textRotation"`
	Indent      int    `json:"indent"`
	ShrinkToFit bool   `json:"shrinkToFit"`
}

func (t *excelTool) Execute(ctx context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	var in excelInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel: decode: %v", err)}, nil
	}

	switch in.Operation {
	case "read":
		return t.read(ctx, logger, in)
	case "write":
		return t.write(ctx, logger, in)
	case "create":
		return t.create(ctx, logger, in)
	case "list_sheets":
		return t.listSheets(ctx, logger, in)
	case "get_info":
		return t.getInfo(ctx, logger, in)
	case "search":
		return t.search(ctx, logger, in)
	case "copy_sheet":
		return t.copySheet(ctx, logger, in)
	case "delete_sheet":
		return t.deleteSheet(ctx, logger, in)
	case "insert_rows":
		return t.insertRows(ctx, logger, in)
	case "insert_cols":
		return t.insertCols(ctx, logger, in)
	case "merge_cells":
		return t.mergeCells(ctx, logger, in)
	case "unmerge_cells":
		return t.unmergeCells(ctx, logger, in)
	case "set_cell_formula":
		return t.setCellFormula(ctx, logger, in)
	case "add_chart":
		return t.addChart(ctx, logger, in)
	case "add_pivot_table":
		return t.addPivotTable(ctx, logger, in)
	case "add_data_validation":
		return t.addDataValidation(ctx, logger, in)
	case "set_style":
		return t.setStyle(ctx, logger, in)
	case "set_column_width":
		return t.setColumnWidth(ctx, logger, in)
	case "set_row_height":
		return t.setRowHeight(ctx, logger, in)
	case "set_conditional_format":
		return t.setConditionalFormat(ctx, logger, in)
	default:
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel: unknown operation %q", in.Operation)}, nil
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func (t *excelTool) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(t.workDir, p))
}

func (t *excelTool) openFile(path string) (*excelize.File, error) {
	return excelize.OpenFile(t.resolvePath(path))
}

func (t *excelTool) getSheetName(f *excelize.File, requested string) string {
	if requested != "" {
		return requested
	}
	return f.GetSheetName(0)
}

// readCellValue returns the display value of a cell, computing formulas via
// CalcCellValue when a formula is present. Falls back to GetCellValue for
// non-formula cells.
func (t *excelTool) readCellValue(f *excelize.File, sheet, cell string) string {
	if formula, err := f.GetCellFormula(sheet, cell); err == nil && formula != "" {
		if result, err := f.CalcCellValue(sheet, cell); err == nil {
			return result
		}
		return formula
	}
	val, err := f.GetCellValue(sheet, cell)
	if err != nil {
		return ""
	}
	return val
}

// getActualDim returns the actual used range of a sheet as "A1:Xn" style string.
// Tries GetSheetDimension first; if it returns "A1" (the default), scans rows
// to compute the actual extent.
func getActualDim(f *excelize.File, sheet string) string {
	dim, _ := f.GetSheetDimension(sheet)
	if dim != "" && dim != "A1" {
		return dim
	}
	rows, err := f.GetRows(sheet)
	if err != nil || len(rows) == 0 {
		return "A1"
	}
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return "A1"
	}
	endCol, _ := excelize.ColumnNumberToName(maxCols)
	return fmt.Sprintf("A1:%s%d", endCol, len(rows))
}

// ── operation handlers ─────────────────────────────────────────────────

func (t *excelTool) read(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel read: open: %v", err)}, nil
	}
	defer f.Close()

	sheet := t.getSheetName(f, in.SheetName)
	if in.MaxRows <= 0 {
		in.MaxRows = 100
	}

	var rows [][]string
	if in.Range != "" {
		parts := strings.Split(in.Range, ":")
		if len(parts) != 2 {
			return tools.Result{IsError: true, Content: "excel read: invalid range format (expected e.g. A1:D10)"}, nil
		}
		start, _ := parseCell(parts[0])
		end, _ := parseCell(parts[1])
		for r := start.row; r <= end.row && len(rows) < in.MaxRows; r++ {
			var row []string
			for c := start.col; c <= end.col; c++ {
				cell, _ := excelize.CoordinatesToCellName(c, r)
				row = append(row, t.readCellValue(f, sheet, cell))
			}
			rows = append(rows, row)
		}
	} else {
		// Full sheet: determine actual dimensions, then iterate cell-by-cell.
		dim := getActualDim(f, sheet)
		parts := strings.Split(dim, ":")
		if len(parts) == 2 {
			start, _ := parseCell(parts[0])
			end, _ := parseCell(parts[1])
			for r := start.row; r <= end.row && len(rows) < in.MaxRows; r++ {
				var row []string
				for c := start.col; c <= end.col; c++ {
					cell, _ := excelize.CoordinatesToCellName(c, r)
					row = append(row, t.readCellValue(f, sheet, cell))
				}
				// Trim trailing empty cells for readability
				for len(row) > 0 && row[len(row)-1] == "" {
					row = row[:len(row)-1]
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
			}
		}
	}

	return tools.Result{Content: formatRows(rows)}, nil
}

func (t *excelTool) write(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel write: open: %v", err)}, nil
	}
	defer func() {
		if err := f.Save(); err != nil {
			// best-effort save; error reported below
		}
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if in.StartCell == "" {
		in.StartCell = "A1"
	}
	if len(in.Data) == 0 {
		return tools.Result{IsError: true, Content: "excel write: data is required"}, nil
	}

	startCol, startRow, err := excelize.CellNameToCoordinates(in.StartCell)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel write: invalid startCell %q: %v", in.StartCell, err)}, nil
	}

	written := 0
	for ri, row := range in.Data {
		for ci, val := range row {
			cell, _ := excelize.CoordinatesToCellName(startCol+ci, startRow+ri)
			if s, ok := val.(string); ok {
				f.SetCellValue(sheet, cell, s)
			} else if f64, ok := toFloat64(val); ok {
				f.SetCellValue(sheet, cell, f64)
			} else {
				f.SetCellValue(sheet, cell, fmt.Sprint(val))
			}
			written++
		}
	}

	if err := f.Save(); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel write: save: %v", err)}, nil
	}

	return tools.Result{Content: fmt.Sprintf("Wrote %d cells to sheet %q starting at %s.", written, sheet, in.StartCell)}, nil
}

func (t *excelTool) create(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f := excelize.NewFile()
	defer f.Close()

	sheets := in.Sheets
	if len(sheets) == 0 {
		sheets = []string{"Sheet1"}
	}
	// excelize.NewFile already creates "Sheet1"; handle the first sheet name
	defaultSheet := f.GetSheetName(0)
	renamedFirst := false
	for i, name := range sheets {
		if i == 0 {
			if name != defaultSheet {
				f.SetSheetName(defaultSheet, name)
			}
			renamedFirst = true
		} else {
			if _, err := f.NewSheet(name); err != nil {
				return tools.Result{IsError: true, Content: fmt.Sprintf("excel create: new sheet %q: %v", name, err)}, nil
			}
		}
	}
	_ = renamedFirst

	path := t.resolvePath(in.FilePath)
	if err := f.SaveAs(path); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel create: save: %v", err)}, nil
	}

	return tools.Result{Content: fmt.Sprintf("Created workbook %q with %d sheet(s): %s", in.FilePath, len(sheets), strings.Join(sheets, ", "))}, nil
}

func (t *excelTool) listSheets(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel list_sheets: open: %v", err)}, nil
	}
	defer f.Close()

	var out strings.Builder
	sheets := f.GetSheetList()
	out.WriteString(fmt.Sprintf("File: %s\nSheets (%d):\n", in.FilePath, len(sheets)))
	for i, s := range sheets {
		idx, _ := f.GetSheetIndex(s)
		dim := getActualDim(f, s)
		out.WriteString(fmt.Sprintf("  %d. %s (dimensions: %s)", i+1, s, dim))
		if idx == f.GetActiveSheetIndex() {
			out.WriteString(" [active]")
		}
		out.WriteString("\n")
	}
	return tools.Result{Content: out.String()}, nil
}

func (t *excelTool) getInfo(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel get_info: open: %v", err)}, nil
	}
	defer f.Close()

	var out strings.Builder
	out.WriteString(fmt.Sprintf("File: %s\n", in.FilePath))

	targetSheets := f.GetSheetList()
	if in.SheetName != "" {
		targetSheets = []string{in.SheetName}
	}

	for _, s := range targetSheets {
		out.WriteString(fmt.Sprintf("\nSheet: %s\n", s))
		dim := getActualDim(f, s)
		out.WriteString(fmt.Sprintf("  Dimensions: %s\n", dim))
		rows, err := f.GetRows(s)
		if err != nil {
			out.WriteString(fmt.Sprintf("  Error reading rows: %v\n", err))
			continue
		}
		out.WriteString(fmt.Sprintf("  Rows: %d\n", len(rows)))
		merged, _ := f.GetMergeCells(s)
		if len(merged) > 0 {
			out.WriteString(fmt.Sprintf("  Merged cells: %d\n", len(merged)))
			for _, m := range merged {
				if len(m) > 0 {
				out.WriteString(fmt.Sprintf("    %s\n", m[0]))
			}
			}
		}
	}
	return tools.Result{Content: out.String()}, nil
}

func (t *excelTool) search(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel search: open: %v", err)}, nil
	}
	defer f.Close()

	targetSheets := f.GetSheetList()
	if in.SheetName != "" {
		targetSheets = []string{in.SheetName}
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("Searching for %q:\n", in.Query))
	found := 0
	for _, s := range targetSheets {
		rows, err := f.GetRows(s)
		if err != nil {
			continue
		}
		for ri, row := range rows {
			for ci, val := range row {
				if strings.Contains(strings.ToLower(val), strings.ToLower(in.Query)) {
					cell, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
					out.WriteString(fmt.Sprintf("  [%s!%s] %s\n", s, cell, val))
					found++
				}
			}
		}
	}
	if found == 0 {
		out.WriteString("  No matches found.\n")
	} else {
		out.WriteString(fmt.Sprintf("  %d match(es) found.\n", found))
	}
	return tools.Result{Content: out.String()}, nil
}

func (t *excelTool) copySheet(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: open: %v", err)}, nil
	}

	if in.TargetFile != "" {
		// Copy to a different file
		targetPath := t.resolvePath(in.TargetFile)
		targetF, err := excelize.OpenFile(targetPath)
		if err != nil {
			f.Close()
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: open target: %v", err)}, nil
		}
		defer targetF.Close()

		srcIdx, err := f.NewSheet(in.TargetSheet)
		if err != nil {
			f.Close()
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: create temp: %v", err)}, nil
		}
		srcSheetIdx, err := f.GetSheetIndex(in.SourceSheet)
		if err != nil {
			f.Close()
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: get source sheet index: %v", err)}, nil
		}
		if err := f.CopySheet(srcSheetIdx, srcIdx); err != nil {
			f.Close()
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: copy: %v", err)}, nil
		}
		// Copy rows between workbooks
		rows, err := f.GetRows(in.TargetSheet)
		if err != nil {
			f.Close()
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: get rows: %v", err)}, nil
		}
		if _, err := targetF.NewSheet(in.TargetSheet); err != nil {
			f.Close()
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: new sheet in target: %v", err)}, nil
		}
		for ri, row := range rows {
			for ci, val := range row {
				cell, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
				targetF.SetCellValue(in.TargetSheet, cell, val)
			}
		}
		if err := targetF.Save(); err != nil {
			f.Close()
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: save target: %v", err)}, nil
		}
		f.Close()
	} else {
		defer f.Close()
		idx, err := f.GetSheetIndex(in.SourceSheet)
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: get source sheet index: %v", err)}, nil
		}
		newIdx, err := f.NewSheet(in.TargetSheet)
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: new sheet: %v", err)}, nil
		}
		if err := f.CopySheet(idx, newIdx); err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: copy: %v", err)}, nil
		}
		if err := f.Save(); err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel copy_sheet: save: %v", err)}, nil
		}
	}

	return tools.Result{Content: fmt.Sprintf("Copied sheet %q to %q.", in.SourceSheet, in.TargetSheet)}, nil
}

func (t *excelTool) deleteSheet(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel delete_sheet: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	if err := f.DeleteSheet(in.SheetName); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel delete_sheet: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Deleted sheet %q.", in.SheetName)}, nil
}

func (t *excelTool) insertRows(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel insert_rows: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	count := in.Count
	if count <= 0 {
		count = 1
	}
	if err := f.InsertRows(sheet, in.RowIndex, count); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel insert_rows: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Inserted %d row(s) at index %d in sheet %q.", count, in.RowIndex, sheet)}, nil
}

func (t *excelTool) insertCols(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel insert_cols: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	count := in.Count
	if count <= 0 {
		count = 1
	}
	colName, err := excelize.ColumnNumberToName(in.ColIndex)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel insert_cols: invalid column index: %v", err)}, nil
	}
	if err := f.InsertCols(sheet, colName, count); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel insert_cols: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Inserted %d column(s) at index %d (%s) in sheet %q.", count, in.ColIndex, colName, sheet)}, nil
}

func (t *excelTool) mergeCells(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel merge_cells: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if err := f.MergeCell(sheet, in.Range, in.Range); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel merge_cells: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Merged cells %s in sheet %q.", in.Range, sheet)}, nil
}

func (t *excelTool) unmergeCells(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel unmerge_cells: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if err := f.UnmergeCell(sheet, in.Range, in.Range); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel unmerge_cells: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Unmerged cells %s in sheet %q.", in.Range, sheet)}, nil
}

func (t *excelTool) setCellFormula(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_cell_formula: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if err := f.SetCellFormula(sheet, in.Cell, in.Formula); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_cell_formula: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Set formula %q in cell %s of sheet %q.", in.Formula, in.Cell, sheet)}, nil
}

func (t *excelTool) addChart(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel add_chart: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if in.Position == "" {
		in.Position = "H1"
	}

	chartType := mapChartType(in.ChartType)
	var series []excelize.ChartSeries
	for _, s := range in.Series {
		cs := excelize.ChartSeries{
			Name:       s.Name,
			Categories: s.Categories,
			Values:     s.Values,
		}
		series = append(series, cs)
	}

	if err := f.AddChart(sheet, in.Position, &excelize.Chart{
		Type:   chartType,
		Series: series,
		Title:  []excelize.RichTextRun{{Text: in.Title}},
	}); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel add_chart: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Added %s chart %q at %s.", in.ChartType, in.Title, in.Position)}, nil
}

func (t *excelTool) addPivotTable(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel add_pivot_table: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	if err := f.AddPivotTable(&excelize.PivotTableOptions{
		DataRange:       fmt.Sprintf("%s!%s", in.DataSheet, in.DataRange),
		PivotTableRange: fmt.Sprintf("%s!%s", in.SheetName, in.PivotRange),
		Rows:            toPivotFields(in.Rows),
		Columns:         toPivotFields(in.Columns),
		Data:            toPivotFields(in.PivotData),
	}); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel add_pivot_table: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Added pivot table at %s in sheet %q.", in.PivotRange, in.SheetName)}, nil
}

func (t *excelTool) addDataValidation(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel add_data_validation: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)

	dv := excelize.NewDataValidation(true)
	switch in.ValidationType {
	case "list":
		dv.SetDropList(unpackListCriteria(in.Criteria))
	case "whole":
		dv.SetRange(1, 100, excelize.DataValidationTypeWhole, excelize.DataValidationOperatorBetween)
	case "decimal":
		dv.SetRange(0.0, 100.0, excelize.DataValidationTypeDecimal, excelize.DataValidationOperatorBetween)
	default:
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel add_data_validation: unsupported validation type %q (use list, whole, or decimal)", in.ValidationType)}, nil
	}
	dv.SetSqref(in.Range)

	if err := f.AddDataValidation(sheet, dv); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel add_data_validation: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Added %s data validation to range %s in sheet %q.", in.ValidationType, in.Range, sheet)}, nil
}

// ── styling operations ──────────────────────────────────────────────────

func (t *excelTool) setStyle(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_style: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if in.Range == "" {
		return tools.Result{IsError: true, Content: "excel set_style: range is required"}, nil
	}

	parts := strings.Split(in.Range, ":")
	if len(parts) != 2 {
		return tools.Result{IsError: true, Content: "excel set_style: invalid range format (expected e.g. A1:D10)"}, nil
	}

	style := excelize.Style{}
	if in.Font != nil {
		style.Font = &excelize.Font{
			Bold:      in.Font.Bold,
			Italic:    in.Font.Italic,
			Underline: in.Font.Underline,
			Family:    in.Font.Family,
			Size:      in.Font.Size,
			Strike:    in.Font.Strike,
			Color:     in.Font.Color,
		}
	}
	if in.Fill != nil {
		style.Fill = excelize.Fill{
			Type:    in.Fill.Type,
			Pattern: in.Fill.Pattern,
		}
		if in.Fill.Color != "" {
			style.Fill.Color = []string{in.Fill.Color}
		}
	}
	if len(in.Border) > 0 {
		for _, b := range in.Border {
			style.Border = append(style.Border, excelize.Border{
				Type:  b.Type,
				Color: b.Color,
				Style: b.Style,
			})
		}
	}
	if in.Alignment != nil {
		style.Alignment = &excelize.Alignment{
			Horizontal:  in.Alignment.Horizontal,
			Vertical:    in.Alignment.Vertical,
			WrapText:    in.Alignment.WrapText,
			TextRotation: in.Alignment.TextRotation,
			Indent:      in.Alignment.Indent,
			ShrinkToFit: in.Alignment.ShrinkToFit,
		}
	}
	if in.NumberFormat != "" {
		style.CustomNumFmt = &in.NumberFormat
	}

	styleID, err := f.NewStyle(&style)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_style: new style: %v", err)}, nil
	}

	if err := f.SetCellStyle(sheet, parts[0], parts[1], styleID); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_style: apply: %v", err)}, nil
	}

	var desc []string
	if in.Font != nil {
		desc = append(desc, "font")
	}
	if in.Fill != nil {
		desc = append(desc, "fill")
	}
	if len(in.Border) > 0 {
		desc = append(desc, "border")
	}
	if in.Alignment != nil {
		desc = append(desc, "alignment")
	}
	if in.NumberFormat != "" {
		desc = append(desc, "number format")
	}
	return tools.Result{Content: fmt.Sprintf("Applied style (%s) to range %s in sheet %q.", strings.Join(desc, ", "), in.Range, sheet)}, nil
}

func (t *excelTool) setColumnWidth(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_column_width: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if in.Col == "" {
		return tools.Result{IsError: true, Content: "excel set_column_width: col is required"}, nil
	}
	if in.Width <= 0 {
		return tools.Result{IsError: true, Content: "excel set_column_width: width must be positive"}, nil
	}

	if err := f.SetColWidth(sheet, in.Col, in.Col, in.Width); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_column_width: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Set width of column %q to %g in sheet %q.", in.Col, in.Width, sheet)}, nil
}

func (t *excelTool) setRowHeight(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_row_height: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if in.Row <= 0 {
		return tools.Result{IsError: true, Content: "excel set_row_height: row must be positive"}, nil
	}
	if in.Height <= 0 {
		return tools.Result{IsError: true, Content: "excel set_row_height: height must be positive"}, nil
	}

	if err := f.SetRowHeight(sheet, in.Row, in.Height); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_row_height: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Set height of row %d to %g in sheet %q.", in.Row, in.Height, sheet)}, nil
}

func (t *excelTool) setConditionalFormat(ctx context.Context, _ *slog.Logger, in excelInput) (tools.Result, error) {
	f, err := t.openFile(in.FilePath)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_conditional_format: open: %v", err)}, nil
	}
	defer func() {
		f.Save()
		f.Close()
	}()

	sheet := t.getSheetName(f, in.SheetName)
	if in.Range == "" {
		return tools.Result{IsError: true, Content: "excel set_conditional_format: range is required"}, nil
	}
	if in.CondType == "" {
		return tools.Result{IsError: true, Content: "excel set_conditional_format: condType is required"}, nil
	}

	opts := excelize.ConditionalFormatOptions{
		Type:     in.CondType,
		Criteria: in.CondOperator,
	}

	// Set criteria value(s)
	if len(in.CondCriteria) > 0 {
		var crit struct {
			Value  string `json:"value"`
			Value2 string `json:"value2"`
		}
		if err := json.Unmarshal(in.CondCriteria, &crit); err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_conditional_format: criteria parse: %v", err)}, nil
		}
		opts.Value = crit.Value
		if crit.Value2 != "" {
			// For "between" criteria, Value is comma-separated or use two fields
			opts.Value = crit.Value + "," + crit.Value2
		}
	}

	// Build a style if condStyle is provided
	if in.CondStyle != nil {
		style := excelize.Style{
			Font: &excelize.Font{
				Bold:   in.CondStyle.Bold,
				Italic: in.CondStyle.Italic,
				Color:  in.CondStyle.Color,
				Size:   in.CondStyle.Size,
				Family: in.CondStyle.Family,
			},
		}
		id, err := f.NewStyle(&style)
		if err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_conditional_format: new style: %v", err)}, nil
		}
		opts.Format = &id
	}

	if err := f.SetConditionalFormat(sheet, in.Range, []excelize.ConditionalFormatOptions{opts}); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("excel set_conditional_format: %v", err)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("Applied %s conditional format to range %s in sheet %q.", in.CondType, in.Range, sheet)}, nil
}

// ── formatting helpers ──────────────────────────────────────────────────

func formatRows(rows [][]string) string {
	if len(rows) == 0 {
		return "(empty)"
	}
	var out strings.Builder
	for _, row := range rows {
		out.WriteString(strings.Join(row, "\t"))
		out.WriteString("\n")
	}
	return out.String()
}

type cellPos struct{ col, row int }

func parseCell(ref string) (cellPos, error) {
	col, row, err := excelize.CellNameToCoordinates(ref)
	if err != nil {
		return cellPos{}, err
	}
	return cellPos{col: col, row: row}, nil
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func mapChartType(t string) excelize.ChartType {
	switch strings.ToLower(t) {
	case "bar":
		return excelize.Bar
	case "line":
		return excelize.Line
	case "pie":
		return excelize.Pie
	case "scatter":
		return excelize.Scatter
	case "area":
		return excelize.Area
	case "doughnut":
		return excelize.Doughnut
	case "radar":
		return excelize.Radar
	default:
		return excelize.Bar
	}
}

func toPivotFields(names []string) []excelize.PivotTableField {
	fields := make([]excelize.PivotTableField, len(names))
	for i, n := range names {
		fields[i] = excelize.PivotTableField{Data: n}
	}
	return fields
}

func unpackListCriteria(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var m struct {
		Values []string `json:"values"`
	}
	if json.Unmarshal(raw, &m) == nil && len(m.Values) > 0 {
		return m.Values
	}
	return nil
}
