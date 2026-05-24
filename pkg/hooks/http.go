package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// runHTTP POSTs the payload to cmd.URL with optional headers + custom
// method (default POST). Returns nil error on 2xx; non-2xx in sync mode
// is logged at warn level and surfaces as a non-blocking error to the
// dispatcher.
//
// Async HTTP hooks (cmd.Async=true) fire in a goroutine and return
// immediately; failure modes are logged but never reach the caller.
func runHTTP(
	ctx context.Context,
	logger *slog.Logger,
	cmd Command,
	payload []byte,
	defaultTimeout time.Duration,
) error {
	if cmd.URL == "" {
		return errors.New("hooks: empty url")
	}
	method := cmd.Method
	if method == "" {
		method = http.MethodPost
	}
	timeout := defaultTimeout
	if cmd.Timeout > 0 {
		timeout = time.Duration(cmd.Timeout) * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, method, cmd.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("hooks: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cmd.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		logger.Warn("hooks.http.error", "url", cmd.URL, "err", err, "elapsed", elapsed)
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	logger.Debug("hooks.http", "url", cmd.URL, "status", resp.StatusCode, "elapsed", elapsed)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hooks: http status %d", resp.StatusCode)
	}
	return nil
}

// runHTTPAsync fires-and-forgets an HTTP webhook.
func runHTTPAsync(
	ctx context.Context,
	logger *slog.Logger,
	cmd Command,
	payload []byte,
) {
	go func() {
		hardCeiling := 30 * time.Second
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hardCeiling)
		defer cancel()
		if err := runHTTP(cctx, logger, cmd, payload, hardCeiling); err != nil {
			logger.Info("hooks.async.http.fail", "url", cmd.URL, "err", err)
		}
	}()
}
