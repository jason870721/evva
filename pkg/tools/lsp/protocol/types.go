// Package protocol holds hand-written LSP protocol types covering the subset
// of the LSP 3.17 spec needed by evva's Phase 1 operations (definition,
// references, hover, document symbols) plus the lifecycle handshake.
package protocol

import "encoding/json"

// ── core geometry ──────────────────────────────────────────────────────

// Position is a zero-based line and character offset in UTF-16 code units.
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range is a span in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a source location — a URI plus a range.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// ── document identifiers ───────────────────────────────────────────────

// TextDocumentIdentifier identifies a text document by URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentPositionParams combines a document identifier with a position.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// TextDocumentItem represents a document that is open in the editor.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int32  `json:"version"`
	Text       string `json:"text"`
}

// ── operation results ──────────────────────────────────────────────────

// Hover holds the result of a hover request.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// MarkupContent is marked-up text (markdown or plaintext).
type MarkupContent struct {
	Kind  string `json:"kind"` // "markdown" or "plaintext"
	Value string `json:"value"`
}

// DocumentSymbol is a hierarchical symbol in a document.
type DocumentSymbol struct {
	Name           string            `json:"name"`
	Detail         string            `json:"detail,omitempty"`
	Kind           SymbolKind        `json:"kind"`
	Range          Range             `json:"range"`
	SelectionRange Range             `json:"selectionRange"`
	Children       []*DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is the older flat symbol representation — some servers
// (or older versions) may return this shape instead of DocumentSymbol.
// textDocument/documentSymbol results are typed json.RawMessage and the
// formatter inspects the JSON to pick the right decoder.
type SymbolInformation struct {
	Name     string     `json:"name"`
	Kind     SymbolKind `json:"kind"`
	Location Location   `json:"location"`
}

// ── symbol kind ────────────────────────────────────────────────────────

// SymbolKind is an LSP symbol kind integer.
type SymbolKind uint32

const (
	SKFile          SymbolKind = 1
	SKModule        SymbolKind = 2
	SKNamespace     SymbolKind = 3
	SKPackage       SymbolKind = 4
	SKClass         SymbolKind = 5
	SKMethod        SymbolKind = 6
	SKProperty      SymbolKind = 7
	SKField         SymbolKind = 8
	SKConstructor   SymbolKind = 9
	SKEnum          SymbolKind = 10
	SKInterface     SymbolKind = 11
	SKFunction      SymbolKind = 12
	SKVariable      SymbolKind = 13
	SKConstant      SymbolKind = 14
	SKString        SymbolKind = 15
	SKNumber        SymbolKind = 16
	SKBoolean       SymbolKind = 17
	SKArray         SymbolKind = 18
	SKObject        SymbolKind = 19
	SKKey           SymbolKind = 20
	SKNull          SymbolKind = 21
	SKEnumMember    SymbolKind = 22
	SKStruct        SymbolKind = 23
	SKEvent         SymbolKind = 24
	SKOperator      SymbolKind = 25
	SKTypeParameter SymbolKind = 26
)

var kindNames = map[SymbolKind]string{
	SKFile: "File", SKModule: "Module", SKNamespace: "Namespace",
	SKPackage: "Package", SKClass: "Class", SKMethod: "Method",
	SKProperty: "Property", SKField: "Field", SKConstructor: "Constructor",
	SKEnum: "Enum", SKInterface: "Interface", SKFunction: "Function",
	SKVariable: "Variable", SKConstant: "Constant", SKString: "String",
	SKNumber: "Number", SKBoolean: "Boolean", SKArray: "Array",
	SKObject: "Object", SKKey: "Key", SKNull: "Null",
	SKEnumMember: "EnumMember", SKStruct: "Struct", SKEvent: "Event",
	SKOperator: "Operator", SKTypeParameter: "TypeParameter",
}

// String returns the human-readable name for a SymbolKind, or "Unknown" for
// unrecognised values.
func (k SymbolKind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "Unknown"
}

// ── call hierarchy ─────────────────────────────────────────────────────

// CallHierarchyItem represents a node in the call graph.
type CallHierarchyItem struct {
	Name           string     `json:"name"`
	Kind           SymbolKind `json:"kind"`
	URI            string     `json:"uri"`
	Range          Range      `json:"range"`
	SelectionRange Range      `json:"selectionRange"`
}

// CallHierarchyIncomingCall represents an incoming call to a function.
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

// CallHierarchyOutgoingCall represents an outgoing call from a function.
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// ── diagnostics ────────────────────────────────────────────────────────

// Diagnostic represents a compiler/linter error or warning.
type Diagnostic struct {
	Range              Range                          `json:"range"`
	Severity           DiagnosticSeverity             `json:"severity,omitempty"`
	Code               string                         `json:"code,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
}

// DiagnosticSeverity is the severity of a diagnostic.
type DiagnosticSeverity uint32

const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

func (s DiagnosticSeverity) String() string {
	switch s {
	case SeverityError:
		return "Error"
	case SeverityWarning:
		return "Warning"
	case SeverityInformation:
		return "Info"
	case SeverityHint:
		return "Hint"
	default:
		return "Unknown"
	}
}

// DiagnosticRelatedInformation is a related location and message.
type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

// PublishDiagnosticsParams is the payload for textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ── response helper ────────────────────────────────────────────────────

// Response is the envelope every JSON-RPC response carries.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError is the JSON-RPC error payload.
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
