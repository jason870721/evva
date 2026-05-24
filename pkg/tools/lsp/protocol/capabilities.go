package protocol

// ── client capabilities (what evva advertises) ─────────────────────────

// ClientCapabilities declares what the client supports.
type ClientCapabilities struct {
	Workspace    *WorkspaceClientCapabilities    `json:"workspace,omitempty"`
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
	General      *GeneralClientCapabilities      `json:"general,omitempty"`
}

// WorkspaceClientCapabilities is empty for Phase 1 — no workspace/configuration
// support is claimed, so servers won't send workspace/configuration requests.
type WorkspaceClientCapabilities struct{}

// TextDocumentClientCapabilities scopes document-related features.
type TextDocumentClientCapabilities struct {
	Synchronization    *SynchronizationCapabilities    `json:"synchronization,omitempty"`
	PublishDiagnostics *PublishDiagnosticsCapabilities `json:"publishDiagnostics,omitempty"`
	Hover              *HoverCapabilities              `json:"hover,omitempty"`
	Definition         *DefinitionCapabilities         `json:"definition,omitempty"`
	References         *ReferencesCapabilities         `json:"references,omitempty"`
	DocumentSymbol     *DocumentSymbolCapabilities     `json:"documentSymbol,omitempty"`
}

type SynchronizationCapabilities struct {
	DidSave bool `json:"didSave,omitempty"`
}

type PublishDiagnosticsCapabilities struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

type HoverCapabilities struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

type DefinitionCapabilities struct {
	LinkSupport bool `json:"linkSupport,omitempty"`
}

type ReferencesCapabilities struct{}

type DocumentSymbolCapabilities struct {
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// ── server capabilities ────────────────────────────────────────────────

// ServerCapabilities is what the LSP server advertises in its initialize
// response. Only the fields relevant to Phase 1 operations are declared;
// unknown fields in the JSON are silently ignored by the decoder.
type ServerCapabilities struct {
	TextDocumentSync       *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	DefinitionProvider     bool                     `json:"definitionProvider,omitempty"`
	ReferencesProvider     bool                     `json:"referencesProvider,omitempty"`
	HoverProvider          bool                     `json:"hoverProvider,omitempty"`
	DocumentSymbolProvider bool                     `json:"documentSymbolProvider,omitempty"`
}

// TextDocumentSyncOptions describes how the server wants documents synced.
type TextDocumentSyncOptions struct {
	OpenClose bool `json:"openClose,omitempty"`
	Change    int  `json:"change,omitempty"` // 0=none, 1=full, 2=incremental
}

// DefaultClientCapabilities builds the capabilities evva declares for every
// LSP server. Positions are always UTF-16 because that's what most servers
// expect; the formatter converts Go offsets to UTF-16 code units.
func DefaultClientCapabilities() ClientCapabilities {
	return ClientCapabilities{
		TextDocument: &TextDocumentClientCapabilities{
			Synchronization: &SynchronizationCapabilities{
				DidSave: true,
			},
			PublishDiagnostics: &PublishDiagnosticsCapabilities{
				RelatedInformation: true,
			},
			Hover: &HoverCapabilities{
				ContentFormat: []string{"markdown", "plaintext"},
			},
			Definition: &DefinitionCapabilities{
				LinkSupport: true,
			},
			References:     &ReferencesCapabilities{},
			DocumentSymbol: &DocumentSymbolCapabilities{HierarchicalDocumentSymbolSupport: true},
		},
		General: &GeneralClientCapabilities{
			PositionEncodings: []string{"utf-16"},
		},
	}
}
