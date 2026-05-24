package protocol

// ── LSP method name constants ──────────────────────────────────────────

const (
	MethodInitialize         = "initialize"
	MethodInitialized        = "initialized"
	MethodShutdown           = "shutdown"
	MethodExit               = "exit"
	MethodDefinition         = "textDocument/definition"
	MethodReferences         = "textDocument/references"
	MethodHover              = "textDocument/hover"
	MethodDocumentSymbol     = "textDocument/documentSymbol"
	MethodDidOpen            = "textDocument/didOpen"
	MethodDidChange          = "textDocument/didChange"
	MethodDidClose           = "textDocument/didClose"
	MethodPublishDiagnostics    = "textDocument/publishDiagnostics"
	MethodCancelRequest         = "$/cancelRequest"
	MethodImplementation        = "textDocument/implementation"
	MethodPrepareCallHierarchy  = "textDocument/prepareCallHierarchy"
	MethodIncomingCalls         = "callHierarchy/incomingCalls"
	MethodOutgoingCalls         = "callHierarchy/outgoingCalls"
	MethodWorkspaceSymbol       = "workspace/symbol"
)

// ── request parameter types ────────────────────────────────────────────

// InitializeParams is sent during the initialize handshake.
type InitializeParams struct {
	ProcessID    int32              `json:"processId"`
	RootURI      string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// InitializeResult is the server's response to initialize.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ReferenceParams extends TextDocumentPositionParams with context.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

// ReferenceContext controls reference search behaviour.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// DidOpenTextDocumentParams for textDocument/didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidCloseTextDocumentParams for textDocument/didClose.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbolParams for textDocument/documentSymbol.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// CancelParams for $/cancelRequest.
type CancelParams struct {
	ID int64 `json:"id"`
}

// CallHierarchyIncomingCallsParams for callHierarchy/incomingCalls.
type CallHierarchyIncomingCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}

// CallHierarchyOutgoingCallsParams for callHierarchy/outgoingCalls.
type CallHierarchyOutgoingCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}

// WorkspaceSymbolParams for workspace/symbol.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}
