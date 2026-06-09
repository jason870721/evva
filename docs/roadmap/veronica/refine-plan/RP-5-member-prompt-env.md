# RP-5 — Swarm 成員提示詞的環境接地：去時間化 ＋ swarm 接地

> 狀態：**草案 / Draft（待 Johnny 拍板）** ｜ 日期：2026-06-06 ｜ 階段：**Phase 1**
> 觸發：第二波 smoke test —— 成員提示詞會接地 OS/workdir（✅ 已有），但**含一行日期**，對連續
> 運行數週/數月的 swarm 既無意義又會在每次重建時漂移。
> 上層設計：[`../veronica-design-v1.md`](../veronica-design-v1.md) ｜ 對照：`internal/agent/sysprompt/fragments.go:144` `environmentSection`。

> **重要更正（2026-06-06，覆蓋本文初稿的錯誤前提）**：初稿宣稱「swarm 成員的提示詞只有 persona ＋
> 團隊協定、完全沒有環境段」。**這是錯的。** 經 source review 確認：swarm 成員是 main-tier persona，
> `agent.New` 會走 `mainProfileFromDiskAgent`（`internal/agent/profiles.go:278`）→
> `sysprompt.ComposeDiskMainPrompt`（`:295`），其輸出 = identity ＋ **environment** ＋（memory）＋ body
> （persona＋團隊協定）＋（skills）。所以成員**已經有** OS/shell/workdir 的環境段。**真正的缺口因此縮小
> 成兩點**：①環境段含一行 `- Today:` 日期（要去掉）；②缺 swarm 專屬接地（space 名、自己的成員名/角色）。

---

## 1. TL;DR

成員提示詞**已經**透過 `ComposeDiskMainPrompt` 接地 OS/shell/workdir（`environmentSection`）。但那段
裡有一行 **`- Today: <日期>`**：

- swarm 會連續運行數週至數月 → 這個日期**很快過時、且無意義**；
- 它在**每次重建**（restart-rebuild / 動態 `AddMember`）時都會變成「當天」→ 破壞 prompt 前綴的
  位元穩定性（cache-friendliness）。

> **方向**：替 swarm 成員**關掉環境段的日期**（其餘 OS/shell/workdir 維持），並**補上 swarm 接地**
> （space 名、自己的成員名與角色）。**無時間 → 提示詞前綴全程位元穩定 → 整個 swarm 生命週期共用同一份
> cache。** 這也與 [RP-7](RP-7-leader-scheduled-wake.md) 互補：時間只在 cron/webhook 的**喚醒 run prompt**
> 出現（`<system-reminder>currenttime: …>`），靜態系統提示詞一律不放時間。

---

## 2. 現況盤點（file:line 證據）

| # | 事實 | 位置 | 意義 |
| --- | --- | --- | --- |
| V1 | 成員是 main-tier persona，走 disk-main 組裝 | `internal/agent/profiles.go:247` → `mainProfileFromDiskAgent`（`:278`） | 成員提示詞 = `ComposeDiskMainPrompt` 的輸出 |
| V2 | 組裝順序含 environment | `sysprompt/fragments.go:261` `ComposeDiskMainPrompt`（`:271-280` joinSections） | identity ＋ **environment** ＋ memory ＋ body ＋ skills ＋ dev |
| V3 | 環境段內容（含日期那行 ← 要去掉） | `fragments.go:144` `environmentSection`；`- Today:`（`:158`） | OS/shell、Today、workdir、AAP_HOME |
| V4 | 注入 body 的成員協定串接點 | `internal/swarm/teamprompt.go:27` `injectTeamProtocol`；注入於 `space.go:148` | body = persona ＋ 團隊協定（**不**含環境，環境由 V2 外層加） |
| V5 | 日期來源 | `environmentSection`：`today := ctx.Today; if today.IsZero() { today = time.Now() }` | 需一個「不要日期」的開關 |
| V6 | swarm 接地可取得的事實 | `space.go`：`sp.cfg.WorkDir`、成員 `name`、`ld.Role`、space `Name` | 可在組裝時補入 |

**結論**：地基比初稿想的更完整（OS/shell/workdir 已在）。只需**去掉日期**＋**補 swarm 接地**——是個
小而精準的改動，不是「新增一整段」。

---

## 3. 設計方向

### 3.1 去時間化：複用共用的 `LongRunning` 旗標（✅ 已拍板）

> **決策（Johnny 2026-06-06）**：RP-5 與 [RP-10-3](RP-10A-subtickets.md) **共用同一個 `LongRunning` 旗標**
> ——「長命/swarm persona ⇒ 去掉會漂移或不該有的 prompt 片段」。RP-10-3 負責**引入**該旗標（def →
> AgentSpec → `sysprompt.AgentDefinition`，並於 `space.go` `registerDef` 設 `def.LongRunning = true`）；
> **RP-5 只加「消費端」**：

- 新增 `PromptContext.OmitDate bool`；`environmentSection`（`fragments.go:144`）為真時略過 `- Today:`。
- `mainProfileFromDiskAgent`（`profiles.go:278`）以 `ctx.OmitDate = def.LongRunning` 驅動（def 已在
  `ComposeDiskMainPrompt` 的簽名內）——swarm persona 自動去日期，evva 主 agent（`LongRunning=false`）
  維持現有 `- Today:`，不受影響。
- 不另立 RP-5 專屬旗標。落地序：RP-10-3 先引入 `LongRunning` → RP-5 接上 `OmitDate` 消費端（若 RP-5
  先落則由 RP-5 引入該欄，RP-10-3 改純消費；無論誰先，只有一個 `LongRunning`）。

### 3.2 補 swarm 接地（選配但建議）

在環境段（或緊鄰處）補上 swarm 專屬事實，讓成員知道「我在哪個團隊、我是誰」：

```
# Environment
- OS / shell: darwin / /bin/zsh
- Working directory: /Users/.../my-project
- Swarm space: vero-tech-swarm
- You are: backend-a (role: worker)
```

- 來源：`space.go` 組裝時已有 `sp.Name`、成員 `name`、`ld.Role`、`sp.cfg.WorkDir`（V6）。
- **刻意不放**：日期、時間、knowledge-cutoff —— 任何隨時間漂移的欄位。
- 實作可走「`injectTeamProtocol` 開頭加一小段 swarm 環境」或「擴 `environmentSection` 認得 swarm
  ctx」；兩者皆可，前者更內聚於 swarm 套件、不動 evva 主線。

### 3.3 快取理由（要寫進註解的 WHY）

去日期後，成員系統提示詞**前綴在整個空間生命週期內位元不變** → 對齊 Anthropic prompt cache 的
穩定前綴；這正是「不要加時間」的工程理由（非美觀問題）。時間需求由喚醒 run prompt 承接
（[RP-7](RP-7-leader-scheduled-wake.md) / [RP-9](RP-9-external-event-webhook.md)）。

---

## 4. Scope / Acceptance

**In**：環境段去日期開關（swarm 啟用、evva 主線不變）；補 swarm 接地（space/name/role）；單元測試。

**Out**：改 evva 主 agent 的環境段預設行為（不動主線）；把時間以任何形式放進靜態提示詞（明確拒絕）；
identity/memory 段的調整（不在本 RP）。

**Acceptance**：
1. 任一 worker 的有效系統提示詞**仍含** OS/shell 與工作目錄，但**不含**任何日期/時間欄位。
2. 提示詞含 swarm 接地（space 名、成員名與角色）。
3. 同一空間重建（restart-rebuild / `AddMember`）兩次產生的環境段**位元相同**（cache 穩定、呼應
   loader `Build` 的 re-callability）。
4. evva 主 agent 的環境段不受影響（仍顯示 `- Today:`）。

---

## 5. 落地任務（建議顆粒）

> **Depends on**：共用的 `LongRunning` 旗標（由 [RP-10-3](RP-10A-subtickets.md) 引入：def 欄位 +
> `registerDef` 設 `def.LongRunning=true`）。RP-5 只加消費端。

| # | 任務 | 落點 |
| --- | --- | --- |
| RP-5-1 | `PromptContext` 加 `OmitDate`；`environmentSection` 據此略過 `- Today:`；`mainProfileFromDiskAgent` 設 `ctx.OmitDate = def.LongRunning` | `internal/agent/sysprompt/fragments.go`、`sysprompt.go`、`internal/agent/profiles.go` |
| RP-5-2 | swarm 組裝成員時補 swarm 接地（space/name/role）（`def.LongRunning=true` 由 RP-10-3 在同一處設） | `internal/swarm/space.go`、`teamprompt.go` |
| RP-5-3 | 測試：含 OS/workdir、**不含日期**、含 swarm 接地、re-callable 位元一致；evva 主線仍含日期 | `sysprompt/*_test.go`、`teamprompt_test.go` |

> 小而高槓桿：把「會漂移的一行日期」拿掉、補上「我屬於哪個團隊」，就讓成員提示詞既接地又
> cache 穩定，零持續成本。
