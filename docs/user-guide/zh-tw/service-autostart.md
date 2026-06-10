# evva service 自動重啟（launchd / systemd）

`evva service` 宿主把每個 swarm 的持久狀態放在磁碟上、啟動時自動續跑
（session、未讀信、membership、alarm）——但 crash 或重開機後，沒有人把
**進程**拉回來。這一頁把這件事交給平台的 supervisor。

一行搞定：

```bash
evva service install-unit          # 已存在時加 --force 覆寫
```

它會偵測平台、寫入下方對應的 unit（指向目前的 `evva` 執行檔），並印出啟用
指令。它自己絕不啟用任何東西。

兩個 unit 跑的都是 `evva service start --foreground`：supervisor 必須直接擁有
server 進程。**不要**讓 supervisor 指向普通的 `evva service start`——那條路會
守護化（父進程立刻退出），supervisor 會以為服務死了而無限重啟震盪。

## macOS — launchd

`~/Library/LaunchAgents/com.evva.service.plist`：

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

把執行檔與 log 路徑改成你的，然後：

```bash
launchctl load -w ~/Library/LaunchAgents/com.evva.service.plist
```

`KeepAlive.SuccessfulExit=false` 表示任何非正常退出（crash、kill -9）都會重啟，
但尊重刻意的停止。要主動停掉請用
`launchctl unload ~/Library/LaunchAgents/com.evva.service.plist`——不要用
`evva service stop`，launchd 會把它當成故障再拉起來。

## Linux — systemd（user unit）

`~/.config/systemd/user/evva-service.service`：

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
loginctl enable-linger $USER     # 開機即啟動，不必等登入
```

啟停用 `systemctl --user stop|start evva-service`；看 log 用
`journalctl --user -u evva-service`。

## 驗證恢復

```bash
kill -9 $(cat ~/.evva/service/evva-service.pid)
sleep 10
curl -s http://127.0.0.1:8888/healthz | jq .
```

supervisor 會在數秒內把宿主拉回來；`/healthz` 回版本、（剛歸零的）uptime 與
space/成員計數，所有已註冊的 swarm 自動 reconcile——運行中的 space 重建、未讀
信重新排隊、被凍結的成員維持凍結、未到期的 alarm 重新上膛。

supervised 模式兩個注意事項：

- 會話 token 每次啟動重新鑄造，所以**每次重啟都會輪換**。本機瀏覽器自動重新
  登入（loopback bootstrap）；其他裝置要去 `~/.evva/service/token` 拿新 token。
- `evva service status` 照常可用（foreground 模式仍寫 pidfile），但生命週期
  指令屬於 supervisor。
