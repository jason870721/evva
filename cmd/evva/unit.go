package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// RP-18: autostart unit templates. The service survives crashes fine
// (resume.go restores sessions, mail, membership, alarms) — what was missing
// is the thing that brings the process back. These templates hand that job to
// the platform's supervisor. Both run `evva service start --foreground`: the
// supervisor must own the process directly; pointing it at the
// self-daemonizing path would have it supervise a parent that exits
// immediately and flap forever.

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>com.evva.service</string>
	<key>ProgramArguments</key>
	<array>
		<string>%[1]s</string>
		<string>service</string>
		<string>start</string>
		<string>--foreground</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key>
	<dict><key>SuccessfulExit</key><false/></dict>
	<key>StandardOutPath</key><string>%[2]s</string>
	<key>StandardErrorPath</key><string>%[2]s</string>
</dict>
</plist>
`

const systemdUnit = `[Unit]
Description=evva swarm service (workstation host)
After=network.target

[Service]
ExecStart=%[1]s service start --foreground
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

// unitFor resolves one platform's unit: where it goes (relative to home), its
// content, and how the operator activates it. Pure, so every platform's
// output is testable from any platform.
func unitFor(goos, exe, logPath string) (relPath, content, activate string, err error) {
	switch goos {
	case "darwin":
		return filepath.Join("Library", "LaunchAgents", "com.evva.service.plist"),
			fmt.Sprintf(launchdPlist, exe, logPath),
			"launchctl load -w ~/Library/LaunchAgents/com.evva.service.plist",
			nil
	case "linux":
		return filepath.Join(".config", "systemd", "user", "evva-service.service"),
			fmt.Sprintf(systemdUnit, exe),
			"systemctl --user daemon-reload && systemctl --user enable --now evva-service\n" +
				"  (to start at boot without a login session: loginctl enable-linger $USER)",
			nil
	default:
		return "", "", "", fmt.Errorf("no autostart unit template for %s — see docs/user-guide/en/service-autostart.md for the manual setup", goos)
	}
}

// serviceInstallUnit writes the current platform's autostart unit pointing at
// THIS binary and prints the activation command. It never enables anything —
// supervising a service is the operator's explicit call.
func serviceInstallUnit(out io.Writer, force bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	rel, content, activate, err := unitFor(runtime.GOOS, exe, logPath())
	if err != nil {
		return err
	}
	path := filepath.Join(home, rel)
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists — pass --force to overwrite", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", path)
	fmt.Fprintf(out, "  binary: %s\n", exe)
	fmt.Fprintf(out, "activate it with:\n  %s\n", activate)
	fmt.Fprintf(out, "the unit runs `evva service start --foreground`; after a crash the supervisor restarts it and the swarm resumes (sessions, unread mail, membership, alarms).\n")
	return nil
}
