# FE-6 — 介入 ＋ 安全：審批/問答 gate、破壞性操作確認、成員控制

> 狀態：**草案 / Draft** ｜ 日期：2026-06-07 ｜ 類型：FE 實作 PRD（介入面）
> 相依：FE-1、FE-3 ｜ 對應：RP-2（審批路由/佇列/重放）、RP-4 §4.3（安全防呆）、H2/H12/H14
> 系列：[FE v2 總覽](README.md)

---

## 1. 目標

做好「**介入**」這半邊：當 agent 需要人（審批 / 問答）或操作者要動危險開關（halt / reset / freeze）時，**清楚、不被綁架、不誤觸、不卡死**。v1 已落地 modal/tray/佇列/重放/ConfirmDialog（RP-2、RP-4 UX-1b/UX-3），v2 token 化重做並**補一個 parity 洞：多選問答**。

---

## 2. 審批/問答 gate

### 2.1 兩種承載（保留偏好）
- **modal（預設）**：阻斷式，強迫處理（`ApprovalOverlay.vue`）。
- **tray（非阻斷側欄）**：邊看團隊邊逐一決定（`ApprovalTray.vue`）。
- 偏好 `uiStore.gateMode` 持久化（沿用 `SpaceView.vue:44-52`）。共用一張 `GateCard`（modal 與 tray 不重複 allow/deny/answer 邏輯，保留 RP-4 UX-1b 的抽法）。

### 2.2 佇列（多成員同時 block）
- `gateStore`（FE-3）以 `(agentId, requestId)` de-dup 佇列；head 處理完下一個自動浮上（`SpaceView.vue:253-260`）。
- **N pending 徽章**：modal 角標 / tray 頂標顯示剩餘數。
- **reconnect 重放 / hydrate**：斷線期間升起的 gate 於 open 時補抓（`gate.hydrate()`，RP-2 §3.3）。

### 2.3 審批卡（approval）
- 顯示：`tool`、`RiskHint`（紅徽）、agentId、`InputDescription`、`Reason`、**plan 內容**（plan-mode 審批的 `PlanContent`，`events.js:317-329`）。
- 動作：**Allow once** / **Always allow**（seed session rule，帶 `ruleTool`，`GateCard.vue:34-41`）/ **Deny**（帶 reason）。
- 危險工具（RiskHint 非空）視覺升級：紅描邊＋需明確點擊（不可 Enter 一鍵過）。

### 2.4 問答卡（question）— **補 multi-select 洞**
wire shape：`Questions[]{ Question, Header, Options[], MultiSelect }`（`events.js:330-337`、`GateCard.vue:11`）。v1 **只渲染單選 radio**（`GateCard.vue:84-88`），對 `MultiSelect:true` 的問題會送錯。v2：

- `MultiSelect:false` → radio（單選）。
- `MultiSelect:true` → checkbox（多選），送出 `answers[question] = [labels]`。
- **「其他」自由輸入**：每題附一個 Other 文字框（對齊 harness `AskUserQuestion` 的 Other 行為），讓 agent 收到自訂答案。
- `Header` 當區塊小標；`Option.Description` 顯在標籤後。
- 多題逐題作答，全部填妥才可 Submit。

> ⚠ **後端 parity 檢查**：確認 `RespondQuestion`（`api.go:575`）與後端 question gate 能接受 multi-value / 自由文字。若後端目前只收單值，另立一張小 backend ticket（不夾帶進 FE，但在此標記為相依風險）。

### 2.5 鍵盤（RP-4 §4.3）
`A`=allow、`D`=deny、`1..9`=選項、`Space`=多選切換、`Enter`=submit、`Esc`=（tray）收起。focus 進 modal 即 trap（`EvDialog`，FE-1）。

### 2.6 per-gate 錯誤（H14）
command_error 帶 `reqId`（`api.go:586-591`）→ 對應到**那張卡**顯「送出失敗，重試」，並 `gate.hydrate()` 讓它重現，而非全域一行紅字（取代 `SpaceView.vue:228-234` 的全域 `err`）。

```
┌─ 🛡 Permission · qa ───────────────────────  2 pending ─┐
│ bash   ⚠ destructive                                     │
│ rm -rf ./dist && npm run build                           │
│ reason: 重建前清掉舊產物                                  │
│ [Allow once]  [Always allow]  [Deny]        A / D / ⏎     │
│ ⚠ 上次送出失敗 — 已重新載入，請再試一次                   │
└──────────────────────────────────────────────────────────┘
```

---

## 3. 破壞性操作：分級確認

統一走 `ConfirmDialog`（FE-1 `EvDialog` 基座，取代原生 `window.confirm`，RP-4 H12）。文案講清**後果與破壞半徑**：

| 操作 | 半徑 | 確認 | 來源 |
| --- | --- | --- | --- |
| `freeze` / `suspend` 單一成員 | 單成員 | 一段確認 | `api.js:52-55` |
| `remove` 成員 | 單成員（可選刪 on-disk 定義） | 一段＋checkbox「刪定義」 | `SpaceView.vue:92-108`、RP-8 |
| `reset` space | 全 space（ledger＋所有 context） | 一段，明示「不可復原」 | `SpaceView.vue:318-327` |
| **`halt all`** | **全團** | **二次確認**（type-to-confirm 或雙按鈕） | `SpaceView.vue:305-314`、RP-4 H2 |

- 全部從主區挪進 `⚙ space menu`（FE-2）/ roster overflow（FE-7），降誤觸。
- 鍵盤：`Enter`=確認、`Esc`=取消（保留 `ConfirmDialog.vue` 行為）；danger 鈕用 `--color-danger`。
- halt 不再是 v1 的「一鍵零確認」（RP-4 H2 的安全不對稱，正式修掉）。

---

## 4. 成員控制（語意定義；UI 入口在 FE-7 Roster）

freeze/unfreeze、suspend/resume、remove、schedule、skills 的**確認與錯誤語意**在此定義（走 §3 的分級確認與 FE-3 的錯誤出口）；按鈕**入口**渲染在 Roster（FE-7），平時收 hover/overflow（降噪，RP-4 H10）。

---

## 5. 元件

```
gates/
  GateLayer.vue        # 依 gateMode 渲染 modal 或 tray；掛 N-pending
  ApprovalModal.vue    # 阻斷式單卡
  ApprovalTray.vue     # 非阻斷佇列側欄
  GateCard.vue         # 共用：approval / question（radio + checkbox + Other）
  QuestionField.vue    # 單題（single/multi/other）
safety/
  ConfirmDialog.vue    # 分級確認（含 type-to-confirm 變體）
```

---

## 6. 驗收

1. 多成員同時 `waiting-approval`：可**邊監看邊逐一審批**，無互蓋、無被鎖死（modal 與 tray 皆可）。
2. **多選問答**正確渲染 checkbox 並回傳多值；單選 radio；每題可填「其他」。
3. 審批可 Allow once / Always allow（seed rule）/ Deny；plan 內容、RiskHint 正確呈現。
4. gate 送出失敗 → **該卡**顯重試並重現，不靠全域紅字、不無聲卡死。
5. `halt all` **二次確認**；`reset`/`remove` 明示後果；無破壞性操作落在主區可誤觸處。
6. 鍵盤可完成審批/問答（A/D/數字/Enter/Esc）。

---

## 7. 子任務

| # | 子任務 |
| --- | --- |
| FE-6a | GateLayer（modal/tray 切換＋N-pending＋hydrate 接 FE-3） |
| FE-6b | GateCard approval（allow/always/deny、plan、risk）＋鍵盤 |
| FE-6c | GateCard question：**single/multi/Other**（補 parity 洞）＋後端相容查核 |
| FE-6d | per-gate 錯誤/重試（reqId 對應） |
| FE-6e | ConfirmDialog 分級（含 halt-all type-to-confirm） |
