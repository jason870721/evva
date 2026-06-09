# RP-12 — Close the advice loop: leader replies "adopted / not (why)"

> 狀態：**已實作 / Implemented（2026-06-09）** ｜ 階段：**Phase 2（swarm collaboration protocol）** ｜ 見文末 §Implemented
> 觸發：Sunday 專案 **milestone-4**（AI 事件驅動永續台）。前身 = Sunday milestone-3 的 RP-C 草案（當時未 file）；milestone-4 把它從「禮貌性回覆」升為**研究台命脈**——研究台的核心迴路就是 *leader 綜合 worker 建議*，迴路不閉合，整個協作學不會。
> 關聯：[`../direction-flat-comms.md`](../direction-flat-comms.md)（operator↔member comms）、`internal/swarm/teamprompt.go`（`leaderProtocol`/`workerProtocol`，本文唯一改動點）、[RP-11](RP-11-event-routing-and-scoped-lever.md)（姊妹）
> 請求者：Sunday（swarm 的*使用者*）。**無新機制、無 Sunday-specific code**——重用 `send_message`，只加一段 leader 協議 prompt。

---

## 1. Problem（observed + milestone-4 放大）

在諮詢型 swarm（Sunday：analyst/risk/reviewer 建議，只有 leader 行動）裡，諮詢成員 `send_message` 把建議交給 leader，然後……沒下文。leader 採納了沒、為什麼，從不回來。諮詢成員是在對虛空喊話：**無法校準、無法改進**，operator 也看不到「建議→行動」的推理連結。現行協議（`internal/swarm/teamprompt.go`：`leaderProtocol`/`workerProtocol`）叫 worker 往上報，**卻沒叫 leader 把決定往下報**。

**milestone-4 放大**：研究台的一輪是「平行蒐證 → leader 綜合 thesis → risk-monitor 對抗式踢館 → leader 拍板」。如果 leader 拍板後不回 analyst「採納你的 funding 判讀 / 沒採納因為 risk 指出 crowding」，那：

- analyst 無法學「我的哪類判讀被信任 / 被打槍」——研究台**學不會**；
- reviewer 事後復盤少了「建議 vs 決定」的連結，playbook 失真；
- 整個 multi-agent 的價值主張（協作 > 單體）站不住——因為協作的回饋邊被切斷。

## 2. Proposal

強化 **leader 協議**（一段 prompt，無新機制——重用 `send_message`）以閉合迴路：

> 當隊友的建議/報告**驅動了（或沒驅動）**一個決定，回覆他結果 + 一句理由（「採納——切到 mean_reversion」／「暫不——等確認，因為 risk 指出 funding 逆風」）。看不到自己輸入有沒有落地的隊友，無法改進。

選配：在 web timeline 把「建議→決定」連結可視化；但**單靠 prompt 改動即可交付行為**。

## 3. Why evva（not Sunday）

這是 **swarm 協作協議（mesh + bus + roles）** 的性質，由 evva 注入每個成員（`teamprompt.go`）。Sunday 對「隊友之間怎麼講話」**沒有發言權**，也不該有。

## 4. Acceptance

- 諮詢成員建議後，leader 協議促成一則簡短決定回覆；e2e 顯示回覆送達建議者。
- 既有 worker→leader 回報無 regression。
- 便宜：一段 prompt + 一個 e2e 斷言；無新 tool、無 schema。

## 5. Notes

- 與 [RP-11](RP-11-event-routing-and-scoped-lever.md) 自然搭配：若非 leader 成員獲授窄 lever，同樣的「說明你做了什麼、為什麼」紀律也適用於它。
- **與 milestone-4 的 structured-output / typed-memory 協同**：若 leader 的「採納與否 + 理由」是結構化的，reviewer 能把它連同 thesis outcome 寫進 playbook 記憶——見 §6。

## 6. 相關請求：請優先既有兩份 PRD（milestone-4 的天然載體）

milestone-4 的研究台會**大量受益**於 evva 已提案、尚未實作的兩份 PRD。非本 RP 範圍，但請排程時一併權衡優先序：

- **`docs/roadmap/PRD/structured-output-tool.md`** — 讓 headless agent 回 typed JSON。研究台的 **thesis**（方向/信念/失效/證據）是結構化物件；structured-output 是它在 swarm 內傳遞/落地的天然載體（否則靠 free-text + Sunday API schema 兜）。
- **`docs/roadmap/PRD/memory-typed-directory.md`** — typed 記憶目錄。reviewer 的 **playbook**（「這種敘事上次怎麼走」）正是 `feedback`/`reference` 型記憶；與 Sunday 的 thesis/outcome 帳本（系統真相）**互補**：Sunday 存事實，agent 記憶存學到的啟發。

> 這兩份是 evva 自己的 roadmap 項目，不是 Sunday 的需求——只是 milestone-4 會是它們的第一個重度使用者，故標注優先價值。

---

## Implemented（2026-06-09）

**Done** —— `internal/swarm/teamprompt.go:leaderProtocol` 已帶「**Close the loop with your team**」段：當隊友的建議/報告驅動（或沒驅動）一個決定，leader 回覆結果 + 一句理由（例「adopted — switching to mean_reversion」/「holding off, because the breakout isn't confirmed」）。這正是本 RP 的提案，且**只注入 leader**（worker 協議不含）。

- **prompt**：`internal/swarm/teamprompt.go`（`leaderProtocol` 的「Close the loop with your team」段）。
- **test**：`internal/swarm/teamprompt_test.go:TestInjectTeamProtocol_RoleSpecific` —— 斷言該段出現在 leader、不出現在 worker。
- 無新機制（重用 `send_message`）、無 schema、無 Sunday-specific code。`go test ./internal/swarm/...` 綠。
