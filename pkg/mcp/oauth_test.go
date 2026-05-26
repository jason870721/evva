package mcp

import (
	"context"
	"errors"
	"testing"
)

func TestOAuthHandler_RunPrompt(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		h := NewOAuthHandler("srv", nil, func(_ context.Context, p OAuthPrompt) (OAuthPromptResult, error) {
			if p.Server != "srv" || p.AuthURL != "https://auth/x" {
				t.Fatalf("prompt payload wrong: %+v", p)
			}
			return OAuthCompleted, nil
		})
		res, err := h.runPrompt(context.Background(), "https://auth/x")
		if err != nil || res != OAuthCompleted {
			t.Fatalf("got (%v,%v), want (Completed,nil)", res, err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		h := NewOAuthHandler("srv", nil, func(context.Context, OAuthPrompt) (OAuthPromptResult, error) {
			return OAuthCancelled, nil
		})
		res, err := h.runPrompt(context.Background(), "https://auth/x")
		if err != nil || res != OAuthCancelled {
			t.Fatalf("got (%v,%v), want (Cancelled,nil)", res, err)
		}
	})

	t.Run("error propagates", func(t *testing.T) {
		sentinel := errors.New("broker boom")
		h := NewOAuthHandler("srv", nil, func(context.Context, OAuthPrompt) (OAuthPromptResult, error) {
			return OAuthCancelled, sentinel
		})
		_, err := h.runPrompt(context.Background(), "https://auth/x")
		if !errors.Is(err, sentinel) {
			t.Fatalf("error should propagate, got %v", err)
		}
	})
}

func TestOAuthHandler_NilPrompt(t *testing.T) {
	h := NewOAuthHandler("srv", nil, nil)
	if _, err := h.SDKHandler(); err == nil {
		t.Fatalf("SDKHandler with nil prompt should error")
	}
	if _, err := h.runPrompt(context.Background(), "u"); err == nil {
		t.Fatalf("runPrompt with nil prompt should error")
	}
}

func TestOAuthHandler_SDKHandlerBuilds(t *testing.T) {
	// With a non-nil prompt, SDKHandler should stand up the callback
	// listener and return a usable auth.OAuthHandler.
	h := NewOAuthHandler("srv", nil, func(context.Context, OAuthPrompt) (OAuthPromptResult, error) {
		return OAuthCompleted, nil
	})
	sdkH, err := h.SDKHandler()
	if err != nil {
		t.Fatalf("SDKHandler: %v", err)
	}
	if sdkH == nil {
		t.Fatalf("SDKHandler returned nil handler")
	}
}
