# RP-14 — Stuck-run watchdog（卡死的 run 要看得見、停得下）

> 狀態：**✅ 已實作（2026-06-10，feature/RP-14-stuck-run-watchdog）** ｜ 日期：2026-06-10 ｜ 波次：**第四波（運營硬化）** ｜ 優先：**P0**
> 觸發：2026-06-10 健康檢查——run 沒有最長時限；busy 成員 timer 跳過該輪（正確）、rescan
> 只 poke idle（正確）——組合結果是**一個被卡死的 tool call 或 LLM 連線會讓成員永久 busy
> 而無人告警**。Sunday 還配 `max_iterations: 99`，放大了最壞情況。
> 上層：[`../health-check-2026-06-10.md`](../health-check-2026-06-10.md) ｜ 互補：[RP-3](RP-3-agent-run-phase-states.md)（細狀態讓「卡在哪」可見，本 RP 讓「卡了」主動報警）

---

## 1. TL;DR

加一個 **run watchdog**：成員 busy 超過 `stall_threshold` → 發 stall 事件＋通知（一次性、
去重）；超過第二閾值 `hard_timeout`（預設關閉）→ 自動 cancel 該 run。取消的安全性是現成
的——`Suspend` 已是確定性 ctx-cancel，且 `runOnce` 的非乾淨退出路徑會把 claimed 信件
unclaim 回 unread 重投（DB is truth），**取消不丟工作**。

## 2. 現況盤點（file:line 證據）

| # | 事實 | 位置 | 意義 |
| --- | --- | --- | --- |
| S1 | run 無 deadline：`runOnce` 直跑到 `safeRun` 返回 | `scheduler.go:167-210` | ❌ 卡死即永久 busy |
| S2 | busy 成員 timer 跳過本輪（RP-7 §3.6 語意） | `scheduler.go:356-364` | ✅ 正確，但等於沒有外力再碰它 |
| S3 | rescan 只 poke idle 成員 | `scheduler.go:301-315` | ✅ 正確，同上 |
| S4 | `Suspend` = 確定性 ctx-cancel ＋ park | `supervisor.go:354-368` | ✅ cancel seam 現成 |
| S5 | 非乾淨退出 → `UnclaimFor` 重投 claimed 信件 | `scheduler.go:202-208` | ✅ 取消不丟 mail |
| S6 | 細 phase 已可見（executing:bash / waiting-approval:…） | `roster.go:94-107`、RP-3 | ✅ 判斷「合法等待 vs 卡死」的素材 |
| S7 | rescanTick 已是每 space 的週期 goroutine | `scheduler.go:284-295` | ✅ watchdog 掛同一 tick，零新 goroutine |
| S8 | settings 擴充點 | `agentdef/manifest.go:55-60` | 閾值放這 |

## 3. 設計方向

### 3.1 計時起點

`memberRun` 加 `runStartedAt time.Time`：`runOnce` 在 `setRun(RunBusy)` 的同一臨界區記下、
run 結束清空（同把鎖，無新競態面）。

### 3.2 偵測：掛在 rescanTick 上

`rescanUnread` 同輪順掃：`busy && now-runStartedAt > stall_threshold` →

1. 發 `KindStall` 事件（payload：member、已運行時長、當前 phase/tool）→ web Attention
   條目（FE-5 已有 stall 概念位）。
2. Bus 給 `user` 與 leader 各一則說明 message（**每個 run 至多一次**——以 runStartedAt
   為 dedup key；leader 卡死時至少 User 還會收到）。

**Phase-aware 豁免**：`waiting-approval` / `waiting-question` 是合法的人為等待，**不算
stall**（或單獨用更長的閾值＋不同事件種類）——否則每個待審批都會誤報。

### 3.3 處置：可選 hard timeout

`hard_timeout` 超過（預設 0＝關閉）→ 等價 `Suspend → Resume`：cancel 當前 run、
unclaim 重投、回 idle 再 poke。事件流記 `KindStallKilled`。預設關閉的理由：合法的長 run
（大檔分析、長 bash）存在，先讓告警跑一陣子校準閾值，再決定要不要開自動處置。

### 3.4 設定

```yaml
settings:
  stall_threshold: 10m   # busy 超過即告警；0 = 關閉
  hard_timeout: 0        # 超過即自動 cancel；0 = 關閉（預設）
```

## 4. 驗收（DoD）

1. fake controller `sleep` 超過閾值 → `KindStall` 事件 ＋ User/leader 各收到一則通知，
   且同一 run 不重複告警。
2. `waiting-approval` 中的成員超時**不**觸發 stall。
3. 開 `hard_timeout` 後：run 被 cancel、claimed mail 回 unread 並在下一輪重投（複用
   RP-1 的既有測試手法驗證不丟信）。
4. 兩閾值皆 0 時行為與現狀完全一致。
5. `-race` 綠燈。

## 5. 非目標

- 自動診斷卡死原因（那是 transcript / RP-17 event log 的事）。
- LLM/工具層的逐請求 timeout 治理（屬 `pkg/llm`、各工具自身的契約，另案）。

---

## 6. 實作記錄與偏離（2026-06-10）

落點：`scheduler.go`（`memberRun.runStartedAt/stallNotified/stallKilled` + `sweepStalls`
+ `notifyStall`/`notifyStallKilled`，並把 RP-13 的通知抽成共用 `notifyOps`）、
`agentdef/manifest.go`（`stall_threshold`/`stall_hard_timeout` 解析，duration 字串、
`"0"`=關、省略=預設 10m、round-trip 無損）。測試：`stall_test.go` 四條（一次告警/豁免/
hard-kill 重試/預設關）+ manifest knob 測試；`-race` 全綠。User guide（zh/en §8）已加
「成本與卡死保險絲」節。

偏離：

1. **掛在 timerTick 而非 rescanTick**（§3.2 原案）：tick 1 秒、已與 sweepBudgetDay 同點，
   測試也已縮短——同樣零新 goroutine，粒度更細。
2. **沒有 `KindStall` 事件**：沿 RP-13 同一裁定——supervisor 合成事件得寫 `sp.out`，
   buffer 滿會堵住 tick goroutine（spaceSink 的阻塞回壓是給 agent goroutine 的設計）；
   通知走持久信（leader 會被喚醒、operator 在 Timeline 看到）+ Warn log。等 FE 真要接
   Attention 再議安全的合成事件通道。
3. 旋鈕命名 `stall_hard_timeout`（原案 `hard_timeout`，加前綴避免歧義）；`PhasePaused`
   （迭代上限）也列入人為等待豁免。
4. **hard-kill 的循環語意**：取消後信件退回未讀、由 rescan 後盾重投（非乾淨退出會結束
   serve 迴圈，重試靠 rescan ≤8s）——同一件事反覆掛住會「每輪 kill + 通知一次」，
   v1 接受（預設關閉；連續 N 次後自動 Suspend 留給校準期後的後續）。
5. 測試腳手架順手修正：`ctlSpace` 補 `Workdir`（避免 Freeze 把 `.vero/` 寫進 CWD）、
   `startSup` 同步縮短 `rescanInterval`。
