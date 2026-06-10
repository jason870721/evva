# RP-18 — Ops 收口（cron 方言文件化、daemon 自動重啟、healthz 擴充）

> 狀態：**✅ 已完成（2026-06-10，feature/RP-18-ops-polish）** ｜ 日期：2026-06-10 ｜ 波次：**第四波（運營硬化）** ｜ 優先：P2
> 觸發：2026-06-10 健康檢查的三個小缺口，單獨都不值一個 RP，收口在一起。
> 上層：[`../health-check-2026-06-10.md`](../health-check-2026-06-10.md)

---

## 1. cron 方言文件化

自寫 cron（`agentdef/schedule.go:86-93`）支援 `*`、`*/n`、`n`、`a-b`、`a-b/n`、逗號清單，
dom/dow 雙限定時為標準 OR 語意；**不支援**秒級欄位、`L`/`W`/`#` 特殊符、`@every`/`@daily`
別名、TZ 欄位（時區語意＝系統本地，已在 v1.4.5-beta.2 寫進 `schedule_set` 工具描述與
Environment 時區行）。

- 落點：user-guide（zh/en）各加一節「schedule 方言」；`evva-swarm.yml` 範例註解同步。
- 順手：`parseCron` 對不支援語法的錯誤訊息點名「不支援 L/W/#/秒級」，少一輪猜。

## 2. daemon 自動重啟模板

service 有 pidfile/log（SPRD-1-9）但 crash 後**不會自己回來**（`cmd/evva/swarm.go:212`
只有手動 `evva swarm run`）。重啟後的恢復路徑（resume.go：session、mail requeue、
membership、alarms）已可靠——缺的只是「有人把它拉起來」。

- 提供 launchd plist（macOS）與 systemd unit（Linux）模板，`KeepAlive`/`Restart=on-failure`；
  放 `docs/user-guide/` 並在 README 連結。
- 可選甜頭：`evva service install-unit` 偵測平台、寫模板、印啟用指令（不自動啟用）。

## 3. /healthz 擴充

現況只回 200（`webapi/api.go:263`）。加：版本、uptime、space 數、各 space 成員數與
frozen 數——一行 curl 能看出「活著但空轉」vs「正常服役」。與 RP-17 的 metrics endpoint
互補（healthz 免 token、不含敏感細節）。

## 4. 驗收（DoD）

1. user-guide 兩語言都有 schedule 方言節；錯誤訊息測試覆蓋不支援語法。
2. 模板在乾淨機器上照文件操作：kill -9 service 後 30 秒內自動回來，swarm 成員恢復
   （resume 路徑既有測試保障）。
3. `/healthz` 回 JSON 含上述欄位，無 token 可讀，不洩漏成員名以外的內容。

## 5. 實作記錄與偏離（2026-06-10）

1. **cron 方言**：`parseCron` 對 `@`-別名、`TZ=` 前綴、6 欄位（秒）、`L/W/#/?`
   特殊符各給點名式錯誤（測試覆蓋九種寫法＋三種合法寫法）；user-guide zh/en §11
   新增「Schedule cron 方言」節（含 OR 語意與本地時區），§5.4 manifest 範例註解
   加方言指引。`?` 一併點名（Quartz 習慣，原 PRD 沒列）。
2. **自動重啟**：做了「可選甜頭」`evva service install-unit`（偵測平台、寫
   launchd plist / systemd user unit、印啟用指令、絕不自動啟用、`--force` 覆寫），
   核心是配套的新 **`evva service start --foreground`** 模式——supervisor 必須
   直接擁有 server 進程；指向會守護化的舊路徑會無限重啟震盪。foreground 仍寫
   pidfile（`status`/`stop` 不說謊）、沿用 env 合約把 addr/allow-remote 傳進
   serviceRun。模板：launchd `KeepAlive.SuccessfulExit=false`、systemd
   `Restart=on-failure` + `RestartSec=5` + `loginctl enable-linger` 提示。
   runbook 放 `docs/user-guide/{en,zh-tw}/service-autostart.md`（含 kill -9
   驗證步驟與「token 每次重啟輪換」注意事項），README 與 swarm guide §8 都連過去。
3. **/healthz**：回 JSON `{status, version, uptimeSecs, spacesRunning,
   spacesStopped, membersActive, membersFrozen}`。**比 PRD 再收緊一步**：完全
   不含 space/成員「名字」——端點免 token 且 `--allow-remote` 下可能對外，
   per-space 細節已有帶 token 的 `/api/swarms` 與 `/api/swarm/{id}/metrics`。
   version 取 ldflags 注入的 `config.Version`（dev build 退回 version.go 常數）。
4. **DoD#2 的容器限制**：本機（linuxkit 容器）無 systemd/launchd，kill -9 自動
   回復未在乾淨機器實測；已實測的是 foreground 模式本身（pidfile=自身 pid、
   healthz JSON 回應、SIGTERM 乾淨退場清檔）與兩個模板的內容測試
   （`unitFor` 純函數對三平台斷言）。實機 30 秒回復驗證留給 Mac 上第一次照
   runbook 操作時順手做。
5. 測試：cron 點名錯誤 ×9、`extractServiceFlags`、`unitFor`（darwin/linux/未知
   平台）、`serviceInstallUnit`（寫檔/拒絕覆寫/--force）、healthz JSON 契約
   （M0 測試改版）＋ 計數測試（running/stopped space、active/frozen 成員）。
   全套 `go test ./...`＋`-race` 綠。
