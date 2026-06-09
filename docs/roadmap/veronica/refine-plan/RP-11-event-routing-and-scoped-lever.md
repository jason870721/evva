# RP-11 — Per-event routing + a scoped (narrow) lever for a non-leader

> 狀態：**已實作 / Implemented（2026-06-09，scoped lever）** ｜ 階段：**Phase 2（swarm topology）** ｜ 見文末 §Implemented
> 觸發：Sunday 專案 **milestone-4**（AI 事件驅動永續台）的研究台拓樸。前身 = Sunday milestone-3 的 RP-B 草案（當時未 file）；milestone-4 把它從「nice-to-have 紓解閥」升為**研究台運作的拓樸需求**。
> 關聯：[RP-9](RP-9-external-event-webhook.md)（外部事件 webhook，已實作；本文用其 `to:` 欄位）、[RP-2](RP-2-permission-broker-routing.md)（permission broker；本文加 scoped 授權）、[RP-12](RP-12-advice-loop-closure.md)（姊妹：閉合建議迴路）
> 請求者：Sunday（swarm 的*使用者*）。**本 RP 不含任何 Sunday-specific code**——是 mesh/bus/permission 的通用 swarm-runtime 機制。

---

## 1. Problem（observed + milestone-4 放大）

RP-9 讓外部事件**全進 leader**（`to:"leader"`），且**只有 leader 能拉 lever**。兩個後果：

1. **單一漏斗**：加密高相關，崩起來所有標的同時發 `risk_breach` → 全擠進一個 leader run（drain B 折疊）。Sunday PRD §5 明言「這可接受**只因快路徑是確定性 Python**」，但同時警告「**任何依賴 leader 做快反應的設計都是壞的**」，並把紓解列為 open（§12.3/§12.7）。
2. **milestone-4 放大**：研究台的事件型別變多——`catalyst`（解鎖/被駭/治理/macro）、`funding_extreme`、`liq_cluster`、`regime_shift`、`risk_breach`。把它們**全塞給 leader 再由 leader 逐一轉派**，徒增延遲與漏斗壓力。自然的拓樸是：**funding/清算 → analyst-flow；catalyst/新聞 → analyst-news；risk_breach → risk-monitor（且它能就地處置）**。

但「路由給 risk-monitor」要**有用**，前提是 risk-monitor 能**行動**——而今天只有 leader 能。

## 2. Proposal（兩個小而互補的能力）

1. **Per-event-type default recipient**：RP-9 的 `to:` 已支援指定收件人（Sunday 端一個欄位）。本 RP 確認此路徑對非 leader 成員可靠喚醒（drain A/B 對任意成員成立），並在 leader 之外**可被既有機制喚醒 + 行動**。
2. **Scoped lever grant（窄 lever）**：讓 operator 透過 config 授權**特定成員**呼叫**一組很窄的** dangerous action——例如 `risk-monitor` 可 `POST /halt`（甚至只能 `mode=safe`）但**不能** `POST /strategy`、`POST /thesis`。機制選項供 evva 權衡：
   - (a) per-member permission allow-rule scope（在 `pkg/permission` / `internal/permission` 的 rule store 加「成員維度」），或
   - (b) roster 增一個介於 `leader` 與 `consulting` 之間的角色，攜帶一組白名單動作。

> **task-ledger 的 leader-only 不變**：這是關於 **levers（對外 HTTP 副作用）**，不是 task 狀態寫入。leader 仍是預設權威與唯一寫 task 帳本者。

## 3. Why evva（not Sunday）

路由與「誰可以行動」是 **swarm topology + permission**，不是交易邏輯。Sunday 只發事件、只暴露 HTTP——它**不該、也無法**編碼「誰被允許 halt」。這正是 multi-agent completeness oracle 的紀律：缺的能力回 evva，不在外部應用 hack。

## 4. Acceptance

- 非 leader 成員可經 config 被授權**恰好一組**窄 dangerous action；其餘 lever 對它仍 ask/deny。
- `risk_breach` 可投遞給 `risk-monitor`，由它**確定性 halt（safe）而無需 leader round-trip**；leader 仍被通知（副本）。
- 多事件同時到不同收件人時，各自的喚醒/folding 正常（drain A/B 對非 leader 成立）。
- task-ledger 的 leader-only 不變量未受影響；無新 Sunday-specific code。

## 5. Notes

- 保持最小——這是相關叢發的**紓解閥**，非重架構。leader 仍是預設權威。
- 與 [RP-12](RP-12-advice-loop-closure.md) 搭配：被授予窄 lever 的成員，同樣適用「做了什麼、為什麼，回報一句」的紀律。
- milestone-4 若此 RP 未即時實作：Sunday 端**降級**為「事件全進 leader、leader 轉派」跑一個月 test，記為已知限制（單漏斗 + 多一跳延遲），不阻擋 test。

---

## Implemented（2026-06-09）

**Scoped lever — done.** Per-member permission scoping + http_request method/url rule matching:

- **matcher**：`pkg/permission/matcher.go:matchHTTPRequest` —— rule pattern `[METHOD ]<url-glob>` 把一條 http_request allow/deny/ask 規則 scope 到方法 + url（例：`http_request(POST http://127.0.0.1:7777/halt)`）。
- **per-member load**：`pkg/permission/loader.go:LoadMember` —— 共用 project/user 規則 **＋** 一個 member-scoped `<agentDir>/permissions.json`，只載進**該成員自己的** store。
- **path**：`internal/swarm/agentdef/member.go:PermissionsPath` —— `agents/{main,sub}/<name>/permissions.json`。
- **wiring**：`internal/swarm/space.go:constructMember` —— 建每個成員自己的 store，經 `agent.Config.PermissionStore` 傳入（無 permissions.json 的成員行為不變，向後相容）。
- **tests**：`pkg/permission/httprequest_test.go:TestHTTPRequestScopedLever`、`loader_test.go:TestLoadMember`、`agentdef/member_test.go:TestPermissionsPath`。

授予 risk-monitor 窄 halt lever 的用法：放 `agents/sub/risk-monitor/permissions.json` =
`{"permissions":{"allow":["http_request(POST http://127.0.0.1:7777/halt)"]}}`。它即可免審批 POST /halt，而 POST /strategy（與其他成員）仍 ask。

**Event routing** 部分：per-event 收件人（`to: "risk-monitor"`）早已由 RP-9 的 `to:` 欄位支援（Sunday 端設定），無需 evva 改動。原本缺的「非 leader 收到事件後能就地行動」正是 scoped-lever 缺口，現已補上。

`go build/vet/test ./...` 全綠（63 packages）。
