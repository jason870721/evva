# Auto-restarting the evva service (launchd / systemd)

The `evva service` host keeps every swarm's durable state on disk and resumes
it on start (sessions, unread mail, membership, alarms) — but nothing brings
the **process** back after a crash or a reboot. This page hands that job to
your platform's supervisor.

The one-liner:

```bash
evva service install-unit          # --force to overwrite an existing unit
```

It detects your platform, writes the matching unit below pointing at the
current `evva` binary, and prints the activation command. It never enables
anything by itself.

Both units run `evva service start --foreground`: the supervisor must own the
server process directly. Do NOT point a supervisor at the plain
`evva service start` — that path daemonizes (the parent exits immediately),
so the supervisor would think the service died and flap forever.

## macOS — launchd

`~/Library/LaunchAgents/com.evva.service.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>com.evva.service</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/local/bin/evva</string>
		<string>service</string>
		<string>start</string>
		<string>--foreground</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key>
	<dict><key>SuccessfulExit</key><false/></dict>
	<key>StandardOutPath</key><string>/Users/you/.evva/service/evva-service.log</string>
	<key>StandardErrorPath</key><string>/Users/you/.evva/service/evva-service.log</string>
</dict>
</plist>
```

Adjust the binary and log paths, then:

```bash
launchctl load -w ~/Library/LaunchAgents/com.evva.service.plist
```

`KeepAlive.SuccessfulExit=false` restarts the service on any non-clean exit
(crash, kill -9) but respects a deliberate stop. To stop it on purpose:
`launchctl unload ~/Library/LaunchAgents/com.evva.service.plist` — not
`evva service stop`, which launchd would treat as a failure to undo.

## Linux — systemd (user unit)

`~/.config/systemd/user/evva-service.service`:

```ini
[Unit]
Description=evva swarm service (workstation host)
After=network.target

[Service]
ExecStart=/usr/local/bin/evva service start --foreground
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now evva-service
loginctl enable-linger $USER     # start at boot, without waiting for a login
```

Stop/start with `systemctl --user stop|start evva-service`; logs with
`journalctl --user -u evva-service`.

## Verifying recovery

```bash
kill -9 $(cat ~/.evva/service/evva-service.pid)
sleep 10
curl -s http://127.0.0.1:8888/healthz | jq .
```

The supervisor restarts the host within seconds; `/healthz` answers with the
version, uptime (freshly reset), and space/member counts, and every registered
swarm reconciles back — running spaces rebuilt, unread mail requeued, frozen
members still frozen, pending alarms re-armed.

Two notes for supervised mode:

- The session token is minted per start, so it ROTATES on every restart. Local
  browsers re-login automatically (the loopback bootstrap); remote devices
  need the new token from `~/.evva/service/token`.
- `evva service status` keeps working (the foreground mode still writes the
  pidfile), but lifecycle commands belong to the supervisor.
