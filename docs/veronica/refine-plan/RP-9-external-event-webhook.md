# RP-9 — 外部事件 Webhook：讓外部應用驅動 swarm leader 開啟工作流

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-06 ｜ 階段：**Phase 2（淨新增能力）**
> 觸發：希望外部應用（範例：跑在 `localhost:7777` 的交易策略 engine）能在出現信號時，
> 透過一個 service API 把事件推給**指定 swarm 的 leader**，由 leader 啟動既有工作流。
> 上層設計：[`../veronica-design-v1.md`](../veronica-design-v1.md)（§5.5 喚醒源）｜ 關聯：[RP-7](RP-7-leader-scheduled-wake.md)（timer 喚醒，本文是 event 喚醒的姊妹）、[`../direction-flat-comms.md`](../direction-flat-comms.md)（operator↔member comms）

> **Auth 註記（Johnny 指示 2026-06-06）**：目前是測試階段，**先不加任何 token / 認證**，安全性不是
> 此刻的優先目標。端點靠 service 只綁 loopback（`127.0.0.1`）這層既有邊界即可。認證強化留待日後
> 若放寬綁定位址再說（見 §6）。本文已據此移除原先的 webhook-token 設計。

---

## 1. TL;DR

設計文件把 agent 的喚醒源定為 `{message, task, timer}`。本 RP 加入**第四種驅動：外部
webhook 事件** —— 但關鍵洞察是：

> **webhook 在機制上「就是一則 message」**。task 指派靠送一則 message 喚醒成員；timer
> （[RP-7](RP-7-leader-scheduled-wake.md)）靠 poke；而**外部事件只要落到 leader 的 mailbox，既有的
> 喚醒/folding 機制就會自動驅動它** —— idle 的 leader 被喚醒（drain A）、忙碌的 leader 把事件
> 折進當前 run（drain B）。`SendUserMessage`（`service.go:925`）已經證明這條路「**不需要任何新
> 編排**」。

所以本 RP 的工作很薄，只有兩件外圍：

1. **一個 ingest 端點** `POST /api/swarm/{ref}/event`：把外部 JSON 事件投遞給該 space 的 leader
   （測試階段不需 token）。
2. **payload → leader prompt 的塑形**：用 `<system-reminder>` 框住事件（含時間、來源），讓 leader
   一眼認得「這是外部觸發、該評估並行動」。

DX 目標（呼應使用者期待）：**engine 端只要加一個 `notify()` 函式**（一個 HTTP POST），就能
event-driven 驅動指定 swarm。

---

## 2. 情境

```
┌─────────────────────────┐            POST /api/swarm/trader/event             ┌───────────────────────────┐
│ 交易策略 engine          │   { "title": "...", "body": "...", "data": {…} }     │ evva service :8888         │
│ localhost:7777          │ ──────────────────────────────────────────────────►│ (127.0.0.1, loopback only) │
│  偵測到價格波動信號 →     │              （測試階段：無 token）                  │   ↓ 解析 ref→space         │
│  呼叫 notify()          │ ◄───────────────── 202 Accepted {messageId} ─────── │   ↓ Bus.Send → leader 信箱  │
└─────────────────────────┘                                                     └───────────────────────────┘
                                                                                       ↓ supervisor 喚醒
                                                           idle → drain A 喚醒 / busy → drain B 折入
                                                                                       ↓
                                                           leader 讀事件 → task_create/assign → 團隊開工
```

兩者都在 loopback（engine :7777、service :8888）→ 同一信任邊界內的**本機整合**。

---

## 3. 現況盤點（file:line 證據）

| # | 事實 | 位置 | 對本 RP 的意義 |
| --- | --- | --- | --- |
| E1 | service 只綁 `127.0.0.1:8888`（不對外） | `service/service.go:48` `DefaultAddr`、`:44-46`（invariant #6） | loopback 就是現階段唯一信任邊界（測試階段足矣） |
| E2 | 「投信給成員並喚醒」的 seam 已存在，sender `"user"`，複用 `Bus.Send` | `service.go:925` `SendUserMessage` | webhook 走同一條路，**零新喚醒邏輯** |
| E3 | role-addressing：`"leader"` → 該 space 唯一 leader 成員名 | `roster.go:233` `ResolveRecipient` | 端點 default 投給 `leader` |
| E4 | space ref 以 **id 或 name** 解析 | `service.go:745` `entry`、`:758` `resolveLocked` | `{ref}` 可填 id 或人類可讀名（如 `trader`） |
| E5 | bus 訊息持久化到 SQLite、重啟 resume、busy 時 drain B 折入 | `space.go:192`（inbox-drainer）、store messages | webhook 天生**可靠投遞**（不漏、重啟續處理） |
| E6 | 既有人類訊息端點（走 root token guard、指定成員） | `webapi/api.go:250` `POST /api/agents/{name}/message`；`tokenGuard`（`:299`） | webhook 是它的「機器版」：免 token、塑形、tag 來源 |
| E7 | 團隊協定教 leader「來自 user 的訊息 = 直接指令」 | `teamprompt.go:60-61` | 需擴一句涵蓋外部事件來源 |
| E8 | router/`Backend` 是唯一 HTTP↔domain 轉譯層 | `webapi/api.go:144` `NewRouter`、`:19` `Backend` | 新端點 + 新 `Backend` 方法落在這 |

**結論**：投遞與驅動是現成的（E2/E3/E5）；本 RP 只補「一個免認證端點 + 一層事件塑形 + 一句 leader 協定」。

---

## 4. 設計方向

### 4.1 端點

```
POST /api/swarm/{ref}/event
Body : { title?, body, data?, to?, idempotency_key? }
→ 202 Accepted { "messageId": "<bus uuid>" }
```

- **測試階段：免 token**。端點**不**掛在 root `tokenGuard` 之下（否則交易 engine 就得拿 root
  token，反而被迫處理認證）。只靠 loopback 邊界。
- `{ref}`：space id **或** name（複用 `entry/resolveLocked`，E4）。未知或非 running → 404/409。
- **非阻斷、立即回**：投上 bus 即回 `202`（leader 在自己的 loop 跑，端點不等它跑完）——
  webhook 來源要的就是 fire-and-forget。
- `messageId` = `Bus.Send` 的 UUID，讓 engine 端可對帳。
- **request body schema（刻意極簡，為 DX）**：

  | 欄位 | 必填 | 說明 |
  | --- | --- | --- |
  | `body` | ✅ | 人類可讀的事件描述（leader 會讀的主文） |
  | `title` | — | 短主旨（→ message subject，例：`BTC volatility spike`） |
  | `data` | — | 結構化 payload（原樣帶入，供 leader/工具進一步解析） |
  | `to` | — | 投遞對象，**預設 `leader`**；可指定某成員名（如專責 `signal-handler`） |
  | `idempotency_key` | — | 去重鍵（§4.3） |

### 4.2 Payload → leader 訊息塑形

把事件投成一則 message，**sender 標為 `"webhook"`**（與 `"user"`/`"system"` 區隔，讓 UI/timeline
辨識來源），body 用 `<system-reminder>` 框住並**加觸發時間**（與 [RP-7](RP-7-leader-scheduled-wake.md) 的
cron 喚醒格式一致）：

```
subject: [event] BTC volatility spike
body:
<system-reminder>
external-event  source=trader-engine  time=2026-06-06 14:30:00
BTC 1m vol > 3σ; price 64210→66800 in 4m
data: {"symbol":"BTC","z":3.4}
</system-reminder>
```

- `time` = 投遞當下（事件驅動需要時間感；同 RP-7，這是 run prompt、不污染系統提示詞快取）。
- `data` 原樣附上（截斷過大 payload，設上限）。

### 4.3 可靠性與節流

- **可靠投遞**（免費繼承自 bus，E5）：事件落地 SQLite、leader 沒讀完不算 read、service 重啟後
  drain 續處理 → 不漏事件。
- **去重**：`idempotency_key` 落 store 唯一鍵；重送同 key 回 `200`（已接收）而非重投，避免
  engine retry 造成重複觸發。
- **突發折疊**：leader 忙碌時，多個事件由 drain B 折進同一個 run（既有行為）→ 天然吸收信號叢發。
- **節流（選配）**：每 space 可設 `settings.webhook_min_interval`，端點對過於密集的事件回
  `429`（防止 1ms 級信號把 leader 打爆）；v1 可先不做、僅留欄位。

### 4.4 一句 leader 協定（E7）

`teamProtocolCommon`（`teamprompt.go`）目前說「來自 user 的訊息 = 直接指令」。補一句：**來自
`webhook`/外部來源的 `<system-reminder> external-event …>` 是觸發信號**——leader 應評估它、必要時
拆解成任務並指派團隊，或判斷無須行動則簡短記錄。避免 leader 把外部事件當成閒聊忽略。

### 4.5 開發者整合（DX —— 使用者強調的重點）

engine 端只要加一個函式（測試階段連 header 都不用）：

```python
# trading engine @ localhost:7777
import requests
EVVA = "http://127.0.0.1:8888/api/swarm/trader/event"

def notify(title, body, data=None):
    requests.post(EVVA, json={"title": title, "body": body, "data": data}, timeout=2)

# 偵測到波動信號時：
notify("BTC volatility spike", "BTC 1m vol > 3σ; 64210→66800 in 4m",
       {"symbol": "BTC", "z": 3.4})
```

對等的 curl（給文件/測試）：

```bash
curl -XPOST http://127.0.0.1:8888/api/swarm/trader/event \
  -d '{"title":"BTC volatility spike","body":"vol>3σ","data":{"symbol":"BTC","z":3.4}}'
```

---

## 5. Scope / Acceptance

**In**：`POST /api/swarm/{ref}/event` 端點（**免 token**）；payload→leader 訊息塑形（system-reminder +
時間 + sender `webhook`）；`idempotency_key` 去重；leader 協定補一句；`Backend.IngestEvent` + service
實作；測試 + 文件（含 engine notify 範例）。

**Out**：任何認證（測試階段明確不做；§6）；對外網開放 / 0.0.0.0 綁定（維持 loopback，invariant #6）；
事件 → 自動 task 的硬編規則（**由 leader 自主決策**，不在端點寫死）；雙向回呼（leader 完成後
callback engine）——可日後另開。

**Acceptance**：
1. engine（或 curl）對 `POST /api/swarm/{ref}/event` 送事件（無 token）→ 回 `202 + messageId`；該
   space 的 **idle leader 被喚醒並開始處理**；**busy leader 把事件折進當前 run**。
2. 事件在 leader 的 transcript/mailbox 與 timeline 可見，標示來源 `webhook` 與觸發時間。
3. 未知/已停止的 space → `404`/`409`；缺 `body` → `400`。
4. 同 `idempotency_key` 重送不重複觸發。
5. service 重啟後，未被 leader 讀取的事件仍會被處理（不漏）。
6. leader 收到 external-event 會評估並（必要時）拆解指派，而非當閒聊忽略。

---

## 6. 日後（非本期）：認證

測試階段先裸跑。日後若要把 service 綁到非 loopback、或讓不信任的外部程式投事件，再加認證——
屆時建議走**每 space 一把窄權限 webhook token**（只能投事件、不能 halt/reset/delete），而非把
能做一切的 root token 交出去。此刻不實作，僅記錄方向。

---

## 7. 落地任務（建議顆粒）

| # | 任務 | 落點 |
| --- | --- | --- |
| RP-9-1 | `POST /api/swarm/{ref}/event` handler（解析、塑形、202+messageId，**不掛 tokenGuard**） | `webapi/api.go` |
| RP-9-2 | `Backend.IngestEvent(ref, evt)`；service 實作（複用 `Bus.Send`，sender `webhook`，role-address `leader`） | `webapi/api.go`、`service/service.go` |
| RP-9-3 | `idempotency_key` 去重（store 唯一鍵）；payload size 上限 | `swarm/store/`、`service/` |
| RP-9-4 | leader 協定補「外部事件 = 觸發信號」一句 | `swarm/teamprompt.go` |
| RP-9-5 | 測試：投遞→喚醒/折入、404/400、去重、重啟不漏 | `service/*_test.go`、`webapi/api_test.go` |
| RP-9-6 | 文件：engine `notify()` 範例 + curl | `docs/veronica/`、`README` |

> 一句話：把「外部事件」收斂成「一則投給 leader 的 message」——喚醒/folding/可靠投遞全部沿用
> 既有機制，新增的只是**一道（測試階段免認證的）入口**與**一層事件塑形**。這是
> [RP-7](RP-7-leader-scheduled-wake.md)（timer 驅動）的姊妹：兩者一起，swarm 就能被「時間」與
> 「外部世界」兩種非人為訊號驅動。
