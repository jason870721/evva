# Veronica — Refine Plans（smoke-test 後的修整計畫）

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-05
> 觸發：`evva swarm` 第一次 smoke test 暴露的三類問題。
> 上層設計：[`../veronica-design-v1.md`](../veronica-design-v1.md) ｜ 路線圖：[`../roadmap.md`](../roadmap.md)
> 相關方向：[`../direction-flat-comms.md`](../direction-flat-comms.md)

---

## 背景

Phase 1（SPRD-1-1 ~ 1-13）已讓 swarm 主幹跑起來，但第一次 smoke test 發現三個會
**直接讓團隊卡死**的問題。三份計畫各對應一個問題，並做了完整 source-code review
（含 file:line 證據）後提出修整方向。**目前是開發階段，允許大動作重構** —— 凡發現
原設計有結構性缺陷處，計畫直接提出重新設計，而非打補丁。

| # | 計畫 | 對應問題 | 嚴重度 | 核心結論 |
| --- | --- | --- | --- | --- |
| [RP-1](RP-1-messaging-reliability.md) | 訊息投遞可靠性 | A↔B 訊息漏收 / 卡 unread | 🔴 高 | `drainStaleHints` 過度清空 mailbox hint → **lost wakeup**；喚醒只靠 chan hint、未對 DB 對帳。改為 **DB 權威的 level-triggered drain**。 |
| [RP-2](RP-2-permission-broker-routing.md) | Permission broker 卡死 | 審批框出不來、agent 卡 busy | 🔴 高（**deterministic**） | 前端用 `AgentID`(UUID) 回傳審批，後端卻用**成員名**查 controller → 路由必失敗、回應被丟掉。**每一次 web 審批都會 hang。** 另含單槽審批互蓋、無 reconnect 重放。 |
| [RP-3](RP-3-agent-run-phase-states.md) | Agent 狀態過粗 | 只有 busy，卡住看不出卡在哪 | 🟡 中 | Roster 只有 `idle/busy/suspended`，且由 supervisor 手動設定。改為**從 event stream 推導**的細狀態（RUNNING / EXECUTING / WAITING_APPROVAL / …），對齊 evva TUI。 |
| [RP-4](RP-4-web-ui-ux.md) | Web UI/UX 檢討 | 介面像「聊天 App」、缺態勢感知 | 🟡 中（設計方向） | 以資深設計師視角做啟發式評估；重定義為「**swarm operations console**」（監看＋介入），優先補 **Attention Bar / 審批 tray / Team Timeline**。**純設計方向文件、未實作。** |

> RP-1~3 是 smoke-test 暴露的**功能 bug 修整**（已實作，見下方狀態）；**RP-4 是另一條
> UI/UX 設計方向 track**（檢討＋方向，尚未動工）。

## 三者的關係（RP-1~3 功能修整）

```
RP-2（修好審批路由 + 並發 + 重放）  ←──診斷靠──┐
                                             │
RP-3（WAITING_APPROVAL 等細狀態）  ──讓 RP-2 的卡死「看得見」
                                             │
RP-1（訊息不再漏 / 不再卡 unread）  ←── 共用「DB 為唯一真相」哲學
```

- **RP-2 是最該先修的** —— 它是 deterministic bug，不是 race，目前任何走 Web 的審批
  都必然卡死。
- **RP-3 是 RP-2 的觀測面** —— 有了 `WAITING_APPROVAL` 細狀態，「卡在審批」這件事在
  UI 上一眼可見，而不是泛泛的 busy。兩者一起做收益最大。
- **RP-1 獨立但同源** —— 訊息可靠性問題與審批無關，但解法同樣是「不要相信記憶體 hint，
  以 SQLite 落地的真相為準」。

## 建議落地順序

1. **RP-2 §1（路由修復）** —— 最小改動、解掉 deterministic hang，先讓 demo 能跑。
2. **RP-1（訊息可靠性重設計）** —— 解掉「漏收 / 卡 unread」，團隊協作才可信。
3. **RP-3（細狀態）** + **RP-2 §2–§5（並發、重放、HOL）** —— 觀測面與韌性，一起收尾。

> 每份計畫的 Acceptance / DoD 皆可獨立驗收；三者無強制先後依賴（除上述建議順序）。

---

## 實作狀態（2026-06-05 落地）

三份計畫**皆已實作於 `feature/veronica`**（build + vet + `-race`（swarm/cmd）測試綠燈、
depcheck clean、web `npm test` + build 完成）。

| 區塊 | 狀態 | 重點落點 |
| --- | --- | --- |
| RP-2 §3.1 路由 | ✅ | `Roster.ControllerRef`（名稱或 AgentID）、`dispatchInbound` 不再吞錯 + `command_error` WS frame；**走 UUID 路徑的回歸測試** |
| RP-1 訊息可靠性 | ✅ | 新 migration `0002_message_claim.sql`（`claimed_at` 三態）+ `ClaimUnread/ClaimOne/SettleClaimed/UnclaimFor`；scheduler 改 **level-triggered** `serve`+`runOnce`、**刪 `drainStaleHints`**、safety `rescanTick`；drain B 改 claim；重啟 `UnclaimFor`；role-addressing |
| RP-3 細狀態 | ✅ | `RunPhase` + `phaseDeriver`（移植 TUI `status.State`）+ sink 推導寫回 roster；`MemberView.Phase/Tool` + `DisplayPhase`；`list_members` / webapi / web roster pill |
| RP-2 §3.2 並發審批 | ✅ | 前端審批/問題改**佇列**、不互蓋 + 「N pending」徽章 |
| RP-2 §3.3 重放 | ✅ | service `gateTracker` + `GET /api/swarm/:id/pending` + 前端 WS reconnect 時 hydrate |
| RP-2 §3.5 防凍結 | ✅ | hub WS `wsWriteTimeout` + 壞連線淘汰 |
| RP-1 §3.6 console 訊息可見化 | ⏸ **deferred** | 純 UI nicety（inter-agent 訊息目前已可在右欄 AgentTranscript mailbox 看到）；留作後續 |

> 設計決策偏離：RP-3 原案建議「移除 supervisor 手動 setRun、全改 event 推導」。實作改為
> **保留 coarse `run`（idle/busy/suspended，supervisor 權威、event-less 測試 controller 也能用）
> ＋ 疊加 event 推導的 fine `phase`**，前端/`DisplayPhase` 合成顯示（suspended 優先 → phase → coarse）。
> 理由：測試以無 event 的 fake controller 驅動，純 event 推導會讓它們失去狀態；coarse + fine 兩層
> 同時相容測試與真實 agent，且 web-`Run` 路徑也因 event 推導而自動正確。

---

## 第二波 refine —— 優化（RP-5 ~ RP-10）

> 狀態：**草案 / Draft（待 Johnny 拍板，尚未實作）** ｜ 日期：2026-06-06
> 觸發：RP-1~4 落地後的下一輪使用回饋 —— 不是「卡死 bug」，而是**長時間運行的規模化、
> 排程自動化、與團隊編組**。依使用者框定分兩階段：Phase 1（RP-5~7）先做、Phase 2（RP-8）
> 接在其後。每份皆已做 source-code review（含 file:line 證據）。

| # | 計畫 | 階段 | 主題 | 一句話 |
| --- | --- | --- | --- | --- |
| [RP-5](RP-5-member-prompt-env.md) | 成員提示詞環境接地 | Phase 1 | OS/env grounding | （**初稿前提已更正**：成員其實已有 env 段）真正缺口是去掉會漂移的 `- Today:` 日期 ＋ 補 swarm 接地（space/name/role）→ 提示詞前綴位元穩定、cache 友善。 |
| [RP-6](RP-6-completed-task-scaling.md) | Completed-task 規模化 | Phase 1 | 分頁/漸進 reload | `completed` 只增不減 → store 加分頁+計數原語；`task_list` 預設只回最近 N + 總數（leader 上下文不膨脹）；Web 看板 completed「最近 5 + 獨立分頁」。 |
| [RP-7](RP-7-leader-scheduled-wake.md) | Leader 主導排程喚醒 | Phase 1 | crontab 工具化 | 排程骨架已存在但是半成品 → 開給 leader `schedule_set/clear`、喚醒注入 `<system-reminder>currenttime: …, #{prompt}</system-reminder>`、`list_members` 常駐顯示班表、執行中跳過本輪、leader 不能改自己的。 |
| [RP-8](RP-8-web-agent-schedule-mgmt.md) | Web 端 Agent/排程管理 | Phase 2 | User 的方向盤 | User 在 Web 管理任一成員（含 leader）班表、動態新增/移除 worker（leader 唯一不可增刪）、協作工具對 User 透明、增刪後系統發事件通知 leader（露 when_to_use）。 |
| [RP-9](RP-9-external-event-webhook.md) | 外部事件 Webhook | Phase 2 | event-driven 入口 | 新增 `POST /api/swarm/{ref}/event`，讓外部應用（如 `localhost:7777` 的交易 engine）把事件推給 leader 觸發工作流；**測試階段免 token**（靠 loopback 邊界）、`<system-reminder>` 塑形、可靠投遞——webhook 機制上「就是一則 message」，沿用既有喚醒/folding。 |
| [RP-10](RP-10-agent-skills-injection-and-web-mgmt.md) | Agent Skills 注入＋Web 管理 | Phase 1＋2 | 動態能力擴容 | 注入機制本就存在、只是被 `advertise_skills:false` 關著：P1 強制注入 skill name+desc ＋ 預設掛 `skill` 工具 ＋ 接對來源；P2 Web 動態增刪 agent skills、reload system prompt（接受 KV cache miss）。**agent 只能載入、不能著作**（紀律）。 |

**相依**：RP-8（Phase 2）依賴 RP-7 的後端 seam（`Supervisor.SetSchedule/ClearSchedule`、roster
schedule 欄位）；RP-9（Phase 2）獨立，但與 RP-7 互補（timer 驅動 vs 外部事件驅動）；RP-10 P2 與 RP-8
並列（皆 Web 動態 reconfigure agent，共用 reload/寫目錄 pattern）；**RP-5 依賴 RP-10 P1**——兩者
**共用一個 `LongRunning` 旗標**（✅ Johnny 2026-06-06；RP-10-3 引入、RP-5 複用：swarm persona ⇒ 去
`- Today:` 日期 ＋ 去 skill 自建引導）；RP-6 獨立。**建議落地序**：RP-10 P1（打開 skill 注入 ＋ 引入
`LongRunning`，地基；sub-tickets 見 [`RP-10A-subtickets.md`](RP-10A-subtickets.md)）→ RP-5（接 `OmitDate`
消費端）→ RP-6（規模化）→ RP-7（排程能力）→ RP-8／RP-9／RP-10 P2（Web 編組、外部事件、skill 增刪，可並行）。

> **喚醒源全景**：設計文件 §5.5 的 `{message, task, timer}` 在這波擴成四種——RP-7 補實
> **timer**（定時自驅）、RP-9 補 **external event**（外部世界驅動）；兩者機制上都收斂成「投一則
> message 給 leader」，故沿用既有喚醒/drain，不新增編排。

**與第一波的關係**：RP-1~4 是「讓團隊**不卡死**」；RP-5~8 是「讓團隊**跑得久、管得動、編得了**」
—— 接續 [RP-4](RP-4-web-ui-ux.md) 把 Web 從 console（監看+介入）再往「**team & schedule 編組台**」推進。

---

## 第三波 refine —— FE v2（Web UI 2.0 重寫）

> 狀態：**草案 / Draft（待 Johnny 拍板，尚未實作）** ｜ 日期：2026-06-07 ｜ 索引：[`fe-v2/README.md`](fe-v2/README.md)

接在 RP-10 之後的 **FE v2 track**：swarm 功能從 RP-1 長到 RP-10，v1 web 是「邊長功能邊補 UI」，已撞到密度與可維護性天花板。第三波**不再補丁 v1，而是平行新建一套 Web UI 2.0**（Vue 3 + TypeScript + Pinia），達 parity 後汰換 v1。三個招牌：① **NEON TOKYO 設計語言**（對齊 TUI 配色）＋可切換主題＋三層 token CSS；② **agent stream 交互**重設計（多 agent 並發串流、tool/thinking/diff 可讀呈現）；③ 正式型別化狀態層（收掉 662 行 god-component）。本波承接 [RP-4](RP-4-web-ui-ux.md) 的「swarm operations console」北極星，把它從「在 v1 上分階段補丁」升級為「設計系統重寫」。

| # | 計畫 | 主題 | 一句話 |
| --- | --- | --- | --- |
| [FE-1](fe-v2/FE-1-foundations-theme-system.md) | 地基：骨架＋token＋主題 | NEON TOKYO | Vue3+TS+Pinia 骨架、三層 token、NEON TOKYO 旗艦＋可切換主題、port 純邏輯層為 `.ts`。 |
| [FE-2](fe-v2/FE-2-app-shell-and-ia.md) | 資訊架構＋App 殼層 | IA / shell | 全域 chrome、region 版面、深連結路由、破壞性操作收進安全選單。 |
| [FE-3](fe-v2/FE-3-realtime-data-layer.md) | 即時資料層 | Pinia / WS | stores＋WS ingest pipeline＋gate 重放＋command 通道，收掉 god-component 的 IO。 |
| [FE-4](fe-v2/FE-4-agent-stream-console.md) | **Agent stream console** | 串流交互 | 多 agent 並發串流、tool/thinking/diff 分型呈現、follow-tail、firehose/focused。 |
| [FE-5](fe-v2/FE-5-situational-awareness.md) | 態勢感知 | 監看 | Attention（含 stall）＋5 態看板 v2＋Team Timeline 多源 feed。 |
| [FE-6](fe-v2/FE-6-intervention-and-gates.md) | 介入＋安全 | gates | 審批/問答 v2（modal/tray、佇列、鍵盤、**multi-select 補洞**）、破壞性操作分級確認。 |
| [FE-7](fe-v2/FE-7-team-composition-and-automation.md) | 團隊編組＋自動化 | 編組台 | Roster v2、成員增刪、排程（cron 預覽）、skills 管理、外部事件可視化。 |
| [FE-8](fe-v2/FE-8-a11y-rwd-and-migration.md) | a11y／RWD／收尾＋遷移 | cutover | WCAG AA、RWD、四態、i18n scaffold、**parity checklist＋cutover（embed 切 web2）**。 |

**架構決策（已拍板 2026-06-07）**：Vue 3 + TypeScript + Pinia｜平行新建 `web2/`、達 parity 後汰換 v1（保留並 port 已測純邏輯層）｜完整 8 份 arc。**建議落地序**：FE-1 →（FE-2 ∥ FE-3）→（FE-4 ∥ FE-5 ∥ FE-6）→ FE-7 → FE-8。

---

## 第四波 refine —— 運營硬化（RP-13 ~ RP-18）

> 狀態：**草案 / Draft（待 Johnny 拍板，尚未實作）** ｜ 日期：2026-06-10
> 觸發：Phase 2（Sunday trading team）24/7 運營一週後的**全面健康檢查**——`-race` 全綠、
> 覆蓋率 71.7%–90.6%、無 correctness 問題；風險集中在「跑了幾週才會痛」的運營維度：
> 成本看不到、卡死沒人報、ledger 只增不刪、事件看過即逝。
> 總綱（含健康檢查結論與 explore track 索引）：[`../health-check-2026-06-10.md`](../health-check-2026-06-10.md)
> （第三波與第四波之間另有 [RP-11](RP-11-event-routing-and-scoped-lever.md)、
> [RP-12](RP-12-advice-loop-closure.md) 兩份已落地的獨立 refine，未列前文索引。）

| # | 計畫 | 優先 | 主題 | 一句話 |
| --- | --- | --- | --- | --- |
| [RP-13](RP-13-member-usage-metering.md) | 成員用量儀表＋預算熔斷 | **P0** | 成本可觀測 | run 邊界計量進 roster 快照；list_members/web 顯示 per-member tokens；超日預算自動 Freeze＋通知 leader/User，跨日自動解凍（標記自帶觸發日，翻日邊緣不可偷）。**✅ 已實作 2026-06-10。** |
| [RP-14](RP-14-stuck-run-watchdog.md) | Stuck-run watchdog | **P0** | 卡死可見 | busy 超閾值發 stall 通知（每 run 一次；等審批/提問/paused 豁免）；可選 `stall_hard_timeout` 自動 cancel——取消不丟信（unclaim 重投既有保障）。**✅ 已實作 2026-06-10。** |
| [RP-15](RP-15-webapi-auth-hardening.md) | WebAPI 認證硬化 | P1 | 安全邊界 | 兌現 `service.go:58` minted-token TODO；非 loopback 顯式 opt-in＋強制 token；webhook 可選 secret（向後相容）。**✅ 已實作 2026-06-10。** |
| [RP-16](RP-16-ledger-retention.md) | Ledger retention | P1 | 只增不刪 | 已讀且過期的 messages / completed tasks 先歸檔（jsonl.gz）再清；`evva swarm vacuum` ＋ 每日自動；活資料絕不動。**✅ 已實作 2026-06-10。** |
| [RP-17](RP-17-durable-event-log.md) | Durable event log ＋ metrics | P2 | 事後可查 | publish 旁路落日切 jsonl（永不回壓 pump）＋ wake/run/abort counters 與 metrics endpoint；是 EX-4 replay 的地基。**✅ 已實作 2026-06-10。** |
| [RP-18](RP-18-ops-polish.md) | Ops 收口 | P2 | 雜項 | cron 方言文件化、launchd/systemd 自動重啟模板、`/healthz` 擴充。**✅ 已實作 2026-06-10。** |

**建議落地序**：RP-13 → RP-14 →（RP-15 ∥ RP-16）→ RP-17 → RP-18。

**與前三波的關係**：第一波讓團隊**不卡死**、第二波讓團隊**跑得久管得動**、第三波讓 operator
**看得清**；第四波讓這一切**運營得起**——成本、卡死、增長、審計。另起的
[explore track（EX-1~6）](../explore/README.md) 則把運營中長出來的模式（外部記憶、遠端
persona、replay 評測…）以 spike 先行驗證，成功才升級成 RP。
