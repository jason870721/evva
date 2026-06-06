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

## 第二波 refine —— 優化（RP-5 ~ RP-8）

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
