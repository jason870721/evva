# EVVAgent — 使用手冊

## 目錄

- [1. 總覽 — TUI 介面一覽](#1-總覽--tui-介面一覽)
- [2. Slash 指令](#2-slash-指令)
  - [/config — 即時設定](#config--即時設定)
  - [/model — 切換提供者/模型](#model--切換提供者模型)
  - [/profile — 切換人格](#profile--切換人格)
  - [/output-style — 溝通風格](#output-style--溝通風格)
  - [/effort — 思考強度](#effort--思考強度)
  - [/resume — 還原先前的工作階段](#resume--還原先前的工作階段)
  - [/rewind — 時光倒帶](#rewind--時光倒帶)
  - [/context 與 /compact — 上下文階梯](#context-與-compact--上下文階梯)
  - [內建技能](#內建技能)
- [3. 快捷鍵](#3-快捷鍵)
- [4. Yank 模式 — 從對話紀錄複製](#4-yank-模式--從對話紀錄複製)
- [5. 對話紀錄搜尋](#5-對話紀錄搜尋)
- [6. 權限系統](#6-權限系統)
  - [權限模式](#權限模式)
  - [計畫模式（`enter_plan_mode` / `exit_plan_mode`)](#計畫模式enter_plan_mode--exit_plan_mode)
  - [工作樹（`enter_worktree` / `exit_worktree`)](#工作樹enter_worktree--exit_worktree)
  - [核准提示](#核准提示)
  - [權限規則](#權限規則)
  - [密鑰遮蔽 — 什麼可以「離開」](#密鑰遮蔽secret-redaction-什麼可以離開)
  - [沙箱化執行 — 指令在**哪裡**執行](#沙箱化執行sandboxed-execution-指令在哪裡執行)
- [7. 子代理與人格](#7-子代理與人格)
  - [動態工作流（選用）](#動態工作流選用-由引擎執行的任務圖)
- [8. Hooks 鉤子](#8-hooks-鉤子)
  - [Hook 設定檔位置](#hook-設定檔位置)
  - [檔案格式](#檔案格式)
  - [事件](#事件)
  - [Payload 與 Decision](#payload-與-decision)
- [9. MCP 伺服器](#9-mcp-伺服器)
  - [設定伺服器](#設定伺服器)
  - [使用 MCP 工具](#使用-mcp-工具)
  - [資源（Resources）](#資源resources)
  - [需 OAuth 授權的伺服器](#需-oauth-授權的伺服器)
  - [把 evva 當成 MCP 伺服器對外提供](#把-evva-當成-mcp-伺服器對外提供)
- [10. 設定參考](#10-設定參考)
  - [evva-config.yml](#evva-configyml)
  - [.env（選用）](#env選用)
  - [CLI 參數](#cli-參數)
- [11. 執行模式 — TUI vs CLI](#11-執行模式--tui-vs-cli)
- [12. 日誌](#12-日誌)
- [13. LSP — 語言伺服器協定支援](#13-lsp--語言伺服器協定支援)
  - [逐步設定（以 Go 為例）](#逐步設定以-go-為例)
  - [驗證 LSP 是否正常運作](#驗證-lsp-是否正常運作)
  - [其他語言設定](#其他語言設定)
  - [手動設定參考](#手動設定參考)
  - [使用方式](#使用方式)
  - [疑難排解](#疑難排解)
- [14. 以 evva 開發 — SDK（開發者指南）](#14-以-evva-開發--sdk開發者指南)
  - [快速開始 — 約 40 行的完整宿主程式](#快速開始--約-40-行的完整宿主程式)
  - [擴充點一覽](#擴充點一覽)
  - [穩定性與延伸閱讀](#穩定性與延伸閱讀)

---

## 1. 總覽 — TUI 介面一覽

```
┌──────────────────────────────────────────────────────────────┐
│ banner box / transcript                                      │
│                                                              │
│  ▶ user prompt                                               │
│  assistant text…                                             │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ ▰ TODOS         (only when non-empty)                        │
│   ▶ wire migration                                           │
├──────────────────────────────────────────────────────────────┤
│ ‹⠹ explorer› ‹▶ writer› ‹✔ reviewer›   ← active sub-agents   │
├──────────────────────────────────────────────────────────────┤
│ overlay panels: /config · /model · /profile · approval · …   │
├──────────────────────────────────────────────────────────────┤
│ > input                                                      │
├──────────────────────────────────────────────────────────────┤
│ ‹⠋ RUN› ◆ EVVA ◆ ▸ model ◆ in N out M ◆ CTX ▰▰▱…▱ 12%       │
└──────────────────────────────────────────────────────────────┘
```

面板在空白時會折疊至零高度。狀態列始終顯示在底部；`EVVA` 格顯示目前人格的名稱（已轉為大寫）——`/profile` 切換後會變成 `NONO`、`MY-PERSONA` 等。

---

## 2. Slash 指令

在輸入框開頭輸入 `/`，畫面會顯示建議面板。隨著你輸入更多字元，列表會依大小寫不敏感的 prefix 比對進行過濾。當輸入內容與某個指令**完全相符**時，該列會變為綠色並顯示 `✓`——按下 Enter 即可執行。

| 按鍵 | 效果 |
| --- | --- |
| `Tab` | 自動補全為高亮的建議選項 |
| `↑` / `↓` | 移動高亮建議選項 |
| `Enter` | 送出當前輸入（若為有效指令則執行） |
| `Esc` | 在此輸入階段關閉建議面板 |

可用指令：

| 指令 | 功能 |
| --- | --- |
| `/config` | 開啟設定表單 |
| `/model` | 切換 LLM 提供者/模型 — **會清除對話歷史** |
| `/profile` | 切換代理人格（evva、nono…）— **會清除對話歷史** |
| `/output-style` | 切換說話風格（default / Explanatory / Learning / 自訂）— **會清除對話歷史** |
| `/effort` | 設定思考強度（low / medium / high / ultra） |
| `/compact` | 壓縮對話紀錄 — 選擇上下文階梯的其中一階 |
| `/context` | 檢視 prompt 的重量分佈 — 可釘選區塊加以保護 |
| `/resume` | 還原此工作目錄下先前的工作階段 |
| `/rewind` | 回復先前的回合 — 還原程式碼、對話，或兩者 |
| `/redactions` | 本次工作階段中被遮蔽的密鑰 — 按 `r` 顯示原值 |
| `/clear` | 開啟新工作階段 — 清空歷史/用量/待辦；舊階段仍可由 `/resume` 還原 |
| `/exit`、`/quit` | 離開 |

使用者安裝的技能（skills）也會出現在此清單中——任何放在 `~/.evva/skills/<name>/SKILL.md` 或 `<workdir>/.evva/skills/<name>/SKILL.md` 的技能都會以 `/<name>` 的形式出現在同一個建議面板。

### /config — 即時設定

開啟一個帶邊框的表單，列出所有可編輯的設定：

```
┌─ /CONFIG ──────────────────────────────────────────────┐
│ ▶ max_iterations           30                          │
│   max_tokens               4096                        │
│   auto_compact_threshold   0.8                         │
│   display_thinking         true                        │
│   fetch_max_bytes          100000                      │
│   tavily_api_key           ****wxyz                    │
│   llm-provider             ▸                           │
│ [↑↓] navigate · [Enter] edit/toggle/open · [Esc] close │
└──────────────────────────────────────────────────────┘
```

各家 LLM 供應商的憑證收在 `llm-provider ▸` 這一列底下——按 `Enter`
進入各供應商的 `api_key` / `api_url` 欄位，按 `Esc` 返回主列表：

```
┌─ /CONFIG ▸ llm-provider ───────────────────────────────┐
│ ▶ anthropic.api_key        (empty)                     │
│   anthropic.api_url        https://api.anthropic.com   │
│   deepseek.api_key         ****wxyz                    │
│   deepseek.api_url         https://api.deepseek.com    │
│   openai.api_key           (empty)                     │
│   openai.api_url           https://api.openai.com      │
│   glm.api_key              (empty)                     │
│   glm.api_url              https://api.z.ai/api/anthr… │
│   ollama.api_url           http://localhost:11434      │
│ [↑↓] navigate · [Enter] edit/toggle · [Esc] back       │
└──────────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 移動游標 |
| `Enter` | 編輯聚焦的欄位（布林值直接切換；`llm-provider` 會展開供應商子列表） |
| `Enter`（編輯器中） | 套用並儲存 |
| `Esc` | 取消編輯、從供應商子列表返回，或在頂層列表關閉面板 |

API 金鑰欄位會開啟密碼遮罩編輯器；貼上功能照常運作（顯示維持遮罩狀態）。

**即時生效**（立即套用）：

- `max_iterations` — 迴圈安全上限
- `display_thinking` — 切換對話紀錄中的思考區塊顯示
- `auto_compact_threshold` — 上下文壓縮的觸發時機

**已儲存但需重新啟動**（需要重建 client / web 工具）：

- `max_tokens`、`fetch_max_bytes`、`tavily_api_key`、所有 `<provider>.api_key`、所有 `<provider>.api_url`

每次編輯都會立即寫入 `~/.evva/config/evva-config.yml`。

#### 在對話中變更設定

你不必自己打開表單 —— 可以直接用自然語言請 evva 讀取或變更設定（「我的
display_thinking 設定是什麼？」「把 auto-memory 關掉」「將 max_iterations
設為 40」）。在底層，模型會使用一個 `config` 工具，它暴露與此表單相同的設定鍵
（`max_iterations`、`display_thinking`、`default_effort`、
`<provider>.api_key` 等），再加上表單沒有的幾個（`default_effort`、
`default_profile`）。

讀取會直接完成、不會跳出提示。寫入則會經過權限提示，顯示為
`Set <key> to <value>`，因此未經你核准不會有任何變更。祕密值讀回時會被遮罩
（`****wxyz`）。此工具無法切換使用中的模型（請用 `/model`）或變更權限模式
（請用 Shift+Tab）。若想停止針對特定設定的詢問，輸入 `/permissions` 並為
`config` 新增一條 allow 規則。

### /model — 切換提供者/模型

開啟一個清單，顯示程式已知的所有 `(provider, model)` 組合，游標預設停在目前使用中的項目上：

```
┌─ /MODEL ─────────────────────────────────────────────────────┐
│ Swapping clears the conversation — provider-specific state   │
│ (thinking signatures) can't carry across providers.          │
│                                                              │
│   ollama / qwen3.6                                           │
│   anthropic / claude-sonnet-4-6                              │
│   anthropic / claude-opus-4-8                                │
│ ▶ deepseek / deepseek-v4-pro  (current)                      │
│   deepseek / deepseek-v4-flash                               │
│   openai / gpt-5.4-mini                                      │
│   openai / gpt-5.5                                           │
│                                                              │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                │
└──────────────────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 瀏覽清單 |
| `Enter` | 切換至高亮的模型 |
| `Esc` | 取消 |

**重要：** 切換模型必定會清除對話。Anthropic 的 `ThinkingSignature` 綁定特定提供者——若帶著舊對話紀錄跨提供者切換，下一次請求會回傳 400 錯誤。新的選擇也會儲存為 `default_provider` + `default_model`，讓下次啟動時直接沿用。

若有執行中的任務則無法切換；請先按 Esc 取消任務，再輸入 `/model`。

### /profile — 切換人格

切換代理的人格——不同的身份、系統提示詞與工具集。內建 `evva`（完整工具包的軟體工程師人格）隨二進位檔附帶；你可以在 `~/.evva/` 底下建立 `agents/<name>/` 目錄來新增其他人格：

```
~/.evva/agents/nono/
├── system_prompt.md   # 人格本體（必要）
├── tools.yml          # { active: [...], deferred: [...] }
└── meta.yml           # { as: [main|subagent|both], when_to_use, inject_memory, advertise_skills }
```

`meta.yml` 欄位：

| 欄位 | 意義 |
| --- | --- |
| `as` | `[main]`、`[subagent]` 或 `[main, subagent]` 之一。`main` 讓人格出現在 `/profile`；`subagent` 讓人格可透過 Agent 工具的 `subagent_type` 列舉呼叫 |
| `when_to_use` | 在選單中顯示於名稱旁邊的一句簡述 |
| `inject_memory` | 為 `true` 時，人格的系統提示詞會收到 `EVVA.md` + `~/.evva/memory/` 索引（以及型別化記憶指引與召回）。預設 `false` |
| `advertise_skills` | 為 `true` 時，人格的提示詞會列出已安裝的技能目錄。預設 `false` |
| `output_style` | 為這個人格釘選一個輸出風格（教學型人格可以釘 `Learning`）。人格啟用期間優先於使用者設定的風格。留空 = 跟隨使用者的 `/output-style` 選擇 |

選單會列出所有 `as:` 包含 `main` 的人格：

```
┌─ /PROFILE ───────────────────────────────────────────────────┐
│ Switching clears the conversation — each persona has its own │
│ system prompt and tool surface.                              │
│                                                              │
│ ▶ evva  (current)  — full-kit software-engineer              │
│   nono             — finance / numbers persona               │
│                                                              │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                │
└──────────────────────────────────────────────────────────────┘
```

切換後對話紀錄會清空、狀態列的人格名稱會更新為新人格的大寫形式，並把新人格儲存為 `default_profile`，讓下次啟動就以該人格開啟。

宣告為 `as: [main, subagent]` 的人格**同時**可從執行中的根代理透過 Agent 工具呼叫——這就是跨人格委派（例如 `evva` 在不離開階段的情況下，將財務問題委派給 `nono`）。

若有執行中的任務則無法切換；請先按 Esc 取消任務，再輸入 `/profile`。

### /output-style — 溝通風格

輸出風格是疊加在**目前人格說話方式**上的薄層，不需要重新定義人格——不用複製工具、模型或人格提示詞。任何風格都能疊在任何 main 層人格上：`nono` 財務人格一樣可以用 `Explanatory`。

```
┌─ /OUTPUT-STYLE ──────────────────────────────────────────────────┐
│ A style overlays how the active persona talks. Switching rebuilds│
│ the system prompt, so the conversation clears.                   │
│                                                                  │
│ ▶ default  (current)  — evva's standard voice — no overlay       │
│   Explanatory         — explains its implementation choices…     │
│   Learning            — pauses and asks you to write small…      │
│   pirate [project]    — everything in pirate speak               │
│                                                                  │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                    │
└──────────────────────────────────────────────────────────────────┘
```

內建風格（移植自 Claude Code）：

| 風格 | 改變什麼 |
| --- | --- |
| `default` | 無疊加——標準聲音 |
| `Explanatory` | 工作過程中加入 `★ Insight` 教學區塊，說明實作決策與 codebase 的模式 |
| `Learning` | 動手練習模式：代理在有意義的設計決策處暫停、加上 `TODO(human)` 標記，請**你**親手寫 2–10 行的小段程式 |

**自訂風格**是單一 Markdown 檔——`~/.evva/output-styles/<name>.md`（使用者層）或 `<workdir>/.evva/output-styles/<name>.md`（專案層；同名時專案層優先）：

```markdown
---
name: pirate
description: everything in pirate speak
keep-coding-instructions: true
---
Respond like a 17th-century pirate captain. Refer to the codebase as "the ship".
```

內文就是風格提示詞。Frontmatter 欄位：`name`（預設取檔名）、`description`（顯示在選單中）、`keep-coding-instructions`：

- `true` — 風格**疊加**在 evva 的撰碼準則之上（`Explanatory`/`Learning` 的形態）。適合仍要寫程式、只調整語氣或教學行為的情境。
- 省略或 `false` — 風格**取代**「Doing tasks」撰碼準則，把這個階段變成不同用途的助手。框架機制（工具協定、權限、環境、記憶）永遠保留。

注意事項：

- 切換會重建系統提示詞，因此對話會清空——與 `/model`、`/profile` 相同。
- 選擇會以 `output_style` 儲存在 `evva-config.yml`；也可以在 `/config` 修改，或請模型改（`config` 工具會驗證名稱）。這兩條路徑在下次 profile 重建時生效；`/output-style` 選單則立即生效。
- 人格可以在 `meta.yml` 用 `output_style:` 釘選自己的風格（見上表）——人格啟用期間以釘選為準。
- Swarm 成員永遠不套用輸出風格——成員的提示詞由操作者定義且必須保持位元穩定。
- 把檔案命名為 `default.md` 會刻意覆蓋內建 default：等於把自訂聲音釘為整台機器（使用者層）或整個 repo（專案層）的常駐風格。

### /effort — 思考強度

調整模型的推理深度。四個等級：

| 等級 | 使用時機 |
| --- | --- |
| `low` | 快速查找、「X 的語法是什麼」 |
| `medium` | 預設——大多數的撰碼任務 |
| `high` | 非簡單的推理、多步驟重構 |
| `ultra` | 架構性決策、難以察覺的 bug 排查 |

各提供者會把這四個等級對應到自己的旋鈕——Anthropic 的 effort 等級、DeepSeek 的 thinking 開關 + 等級、OpenAI 的 reasoning effort、GLM 的兩段 thinking 等級（low/medium → High、high/ultra → Max）等。對於只有粗略開/關開關的提供者，`low` → 關閉，其餘 → 開啟。所選的等級會儲存為 `default_effort`，並顯示在狀態列上（`▸ model · ⚡high`）。

### /resume — 還原先前的工作階段

從目前的工作目錄還原先前的工作階段。每次迭代的狀態都會持久化到 `~/.evva/sessions/<workdir-slug>/<session-id>.json`，所以關閉 TUI 再重新開啟並不會遺失工作——`/resume` 會把對話帶回你離開時的狀態。

選單以每頁 10 筆、依最後寫入時間遞減排序的方式列出最近活動的工作階段。每一列以一行預覽顯示該階段的第一個使用者提示，並附上人格、訊息數量與模型：

```
┌─ /RESUME ────────────────────────────────────────────────────┐
│ 還原先前的工作階段 — 僅限同一工作目錄，依最近寫入時間遞減。  │
│ 還原會清除目前的對話畫面，並以儲存的版本取代。               │
│                                                              │
│ ▶ 串接 /resume slash 指令與 overlay                          │
│     5m ago · evva · 42 msgs · claude-opus-4-8                │
│   移植型別化記憶目錄 + 相關性召回                            │
│     2h ago · evva · 87 msgs · claude-opus-4-8                │
│   驗證跨平台 release 工作流                                  │
│     1d ago · evva · 18 msgs · deepseek-v4-pro                │
│   …                                                          │
│                                                              │
│ page 1 / 3                                                   │
│ [↑↓] 游標 · [←→] 翻頁 · [Enter] 還原 · [Esc] 取消             │
└──────────────────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 在當前頁面移動游標 |
| `←` / `→` | 切換到前一頁／下一頁（每頁 10 筆） |
| `Enter` | 還原所選的工作階段 |
| `Esc` | 取消 |

**還原時會還原什麼：**

- 完整的訊息歷史——每個使用者提示、助理回應、思考區塊、工具呼叫與工具結果都會重新放入對話畫面，你可以往上捲動查看先前的工作內容。
- 該階段使用的人格、提供者與模型。若這些已不存在（人格被刪除、目前的 build 沒有該模型），則會回退到 `evva` 或目前的預設，並在日誌中記錄警告。
- session-id——後續儲存會覆蓋同一個檔案而非新增，所以還原後的階段在選單中仍維持單一條目。
- 狀態列上的累計用量（usage）與 context 條。

**作用範圍：** 工作階段以發起時的工作目錄為界。在不同目錄下執行 `evva` 會顯示該目錄的工作階段；全部的工作階段儲存在 `~/.evva/sessions/`，並依 workdir slug 分類（例如 `-Users-alice-lab-myrepo`）。

**儲存頻率：** 每次迴圈迭代（即每次工具來回）後都會重寫檔案，所以即使 evva 崩潰，最多只會遺失一次 LLM 呼叫的工作量。

**壓縮行為：** 執行完整的 `/compact` 會以壓縮後的摘要覆蓋同一個工作階段檔案——選單依然只顯示一筆，但內容變成摘要而非原始對話。

**子代理：** 只有根代理的工作階段會被持久化。透過 Agent 工具產生的子代理依設計為短暫的，永遠不會出現在 `/resume` 中。

若有執行中的任務則無法還原；請先按 Esc 取消任務，再輸入 `/resume`。

### /rewind — 時光倒帶

> **預設關閉（opt-in）。** 檢查點/倒帶功能**預設為關閉**——在 `~/.evva/config/evva-config.yml` 設定 `enable_checkpoints: true` 來啟用。在那之前 `/rewind` 不會記錄也不會顯示任何內容。

`/rewind` 用來回復一個走偏的回合——一次糟糕的多檔重構、一次誤判的重寫，或一個把代理帶往錯誤方向的提示。在每個使用者回合開始時，evva 會記錄一個**檢查點（checkpoint）**；當該回合的 `edit` / `write` 工具第一次動到某個檔案時，會擷取該檔案當下的原始位元組（它的*前像 before-image*）。`/rewind` 會列出這些檢查點，並把**程式碼**、**對話**，或**兩者**還原到所選的時間點。

```
┌─ /REWIND ────────────────────────────────────────────────────┐
│ Time-travel undo — restore files, the conversation, or both  │
│ to a prior turn. A code restore overwrites your working tree.│
│                                                              │
│ ▶ refactor the auth middleware to use the new token store    │
│     8m ago · 5 file(s) · conversation ✓                      │
│   add rate-limiting to the public API                        │
│     34m ago · 2 file(s) · conversation ✓                     │
│   draft the release notes                                    │
│     2h ago · 0 file(s) · conversation ✗ (compacted)          │
│                                                              │
│ [↑↓] cursor · [Enter] choose · [Esc] cancel                  │
└──────────────────────────────────────────────────────────────┘
```

選一個檢查點，再選還原模式：

| 模式 | 效果 |
| --- | --- |
| **both** | 還原已擷取的檔案**並**把對話倒帶到該回合之前 |
| **code** | 只還原已擷取的檔案——回寫每個前像，刪除該回合新建的檔案 |
| **chat** | 只把對話倒帶；工作目錄維持不變 |

任何會改寫檔案的模式都會先要求確認——程式碼還原會**覆蓋你的工作目錄**（並刪除該回合之前不存在的檔案）。只倒帶對話不需確認；它只是截斷現有歷史並重繪對話。

**檢查點擷取了什麼**

- 對話切點——回合開始時的訊息歷史長度。
- 該回合 `edit` / `write` 工具動到的每個檔案的前像，於第一次接觸時以原始位元組擷取，因此還原能精確重現原本的編碼與行尾。
- 該回合*新建*的檔案會被記為「原本不存在」，因此程式碼還原會在回復時將它刪除。

**限制與範圍**

- **由 `bash` 改動的檔案不會被擷取。** 此 hook 只看得到 `fs` 工具（`edit`、`write`）；`sed -i`、`>` 重導向、`mv` 或建置產物對 rewind 都是隱形的。請把工作交給 git 作為後盾。
- **跨壓縮的對話倒帶會被停用。** 一次完整的 `/compact` 會把整段歷史改寫成摘要，因此在它之前取得的切點不再指向真實的邊界。那些檢查點會顯示 `conversation ✗ (compacted)`，只提供程式碼還原。
- **僅限單一主代理。** 子代理與 swarm 成員不做檢查點——它們的隔離本身已限制了影響範圍。
- 有執行中的任務時無法 rewind；請先按 Esc 取消。

**儲存與保留**

檢查點存放於 `<workdir>/.evva/checkpoints/<session-id>/`——屬於 evva 自有的執行期狀態，與 `.evva/plans`、`.evva/worktrees` 同類。**請把 `.evva/`（或至少 `.evva/checkpoints/`）加入你的 `.gitignore`。** 每個工作階段保留最近的 `checkpoint_max_per_session` 個檢查點（預設 50；較舊的會被清除，其獨有的前像也會被回收），且跨工作階段只會保留有限數量的命名空間。此功能**預設關閉（opt-in）**（`enable_checkpoints`，預設 off）——請見本節開頭的說明。

### /context 與 /compact — 上下文階梯

每一回合都會把整段對話送給模型，所以長時間的工作階段遲早會撐爆視窗。evva 用**三階梯度**處理這件事，最便宜的先上，而且只有在前一階確實沒能把 prompt 壓回預算內時，才會往上爬：

| 階 | 代價 | 做什麼 | 失去什麼 |
| --- | --- | --- | --- |
| **Prune（修剪）** | 免費，不呼叫 LLM | 把又大又舊的工具結果換成一行墓碑 | 沒有永久損失 — 墓碑會說明怎麼取回 |
| **Span（區段）** | 一次 LLM 呼叫 | 只摘要**最舊的那一半**，近期回合保持原文 | 工作階段早期的細節 |
| **Full（全量）** | 一次 LLM 呼叫 | 把整段對話摘要成一份簡報 | 整份對話紀錄 |

當 prompt 超過 `auto_compact_threshold`（預設為模型視窗的 0.8）時，階梯會自動啟動。`/compact` 則讓你手動觸發任何一階。

**墓碑是指令，不是墓誌銘。** 被修剪的結果會被換成模型可以據此行動的文字：

```
[pruned to save context: read loop.go result from turn 12, 41.2KB — read the file again if you still need it]
```

它說明了被移除的是什麼、原本多大（讓模型自行判斷值不值得取回），以及取回它的確切動作。這正是修剪之所以安全的原因：模型永遠不必猜測某段內容是否曾經存在。

**永遠不會被修剪的東西：**

- **錯誤結果。** 重跑一個失敗的指令有可能第二次就成功，反而抹掉了證據 — 所以錯誤訊息是唯一真正無法靠重跑取回的工具輸出。
- **近期視窗** — 最近 3 個回合，外加不論回合數的最後 12 筆存活結果，這樣即使某一回合一次展開 40 個平行呼叫，模型仍有立足點。
- **低於大小門檻的內容**（2KB），在這個尺度下墓碑犧牲的清晰度比省下的位元組還多。
- **被釘選的區塊**（見下）。

**`/context`** 顯示重量究竟落在哪裡 — 分類統計（系統提示 / 你的回合 / 模型輸出 / 檔案讀取 / 其他工具輸出）以及最重的個別區塊：

```
▰ /CONTEXT — where the prompt's weight sits
prompt · 120,000 / 200,000 tokens  (60.0% of the window)
ledger · 64.7KB across 7 turn(s)

  category         bytes   share
  system          12.0KB   18.5%  persona prompt + tool schemas
  file            41.0KB   63.4%  file reads · cheapest to recover
  tool            11.7KB   18.1%  other tool output

    turn  tool      subject                    bytes
  ▸ 4     read      loop.go                   41.0KB
    6     bash      go                         8.4KB
  ◌ 7     grep      func New                   3.0KB
```

`📌` 代表已釘選，`✘` 代表失敗，`◌` 代表已被修剪。

**釘選。** 選中一個區塊後按 `Space` 即可釘選。被釘選的區塊不受任何一階影響 — 而且當全量壓縮丟棄整份對話紀錄時，釘選的內容會在簡報之後原文重新注入。適合用在整個任務所繫的那一個檔案或指令輸出上。只有工具結果可以釘選；你自己的回合本來就不會被修剪。釘選狀態可以跨 `/resume` 保留。

調校：

```yaml
auto_compact_threshold: 0.8   # 觸發階梯的視窗佔比
context_prune: true           # opt-out；false 會關閉免費的那一階
context_span: true            # opt-out；false 會讓階梯退化成 prune → full
prune_min_bytes: 2048         # 小於此值的結果永不加墓碑
prune_keep_turns: 3           # 永不修剪的近期回合數
prune_keep_results: 12        # 不論回合數，保留的末端存活結果數
```

兩個階梯開關都是 opt-*out*，理由和密鑰遮蔽相同：一個安安靜靜待在預算內的工作階段，勝過一個撞上全量壓縮、把對話紀錄整份弄丟的工作階段。三個整數把 `0` 視為「用預設值」，所以只填一半的設定區塊會退化成安全值，而不是變成一份把對話從模型腳下修剪掉的策略。

### 內建技能

evva 內建五個**內建技能（bundled skills）**——由官方提供、代理可呼叫的指令文件。當請求符合時模型會自動使用它們，你也可以自行輸入 `/<name>` 來呼叫：

| 技能 | 用途 |
| --- | --- |
| `/commit` | 為目前的變更草擬並建立 git commit，以 evva 作為作者。 |
| `/review` | 審查 GitHub pull request（使用 `gh`）。 |
| `/security-review` | 針對分支待提交變更進行聚焦式安全審查。 |
| `/simplify` | 三位審查者並行清理（重用／品質／效率），接著套用修正。 |
| `/setup-hooks` | 引導你在 `.evva/settings.json` 中撰寫生命週期掛鉤（見第 8 節）。 |

內建技能是**最低優先序**的層級：在 `~/.evva/skills/<name>/SKILL.md` 或 `<workdir>/.evva/skills/<name>/SKILL.md` 放置**同名**的 `SKILL.md`，即可無聲覆蓋內建內容。技能在啟動時載入——新增或編輯後請重新啟動 evva。關於撰寫自訂技能與 SDK 方式，請參見[以 evva 開發](#13-以-evva-開發--sdk開發者指南)與 `docs/extending.md`。

---

## 3. 快捷鍵

| 按鍵 | 效果 |
| --- | --- |
| `Enter` | 送出 |
| `Ctrl+J` / `Alt+Enter` | 插入換行（多行輸入） |
| `↑` / `↓` | 瀏覽提示歷史（輸入框為空或已在瀏覽時） |
| `Esc` | 取消執行中的任務 / 關閉面板 |
| `Ctrl+C` | 按一次：取消執行中任務 · 閒置時：離開 |
| `Ctrl+D` | 離開（輸入框為空時） |
| `Ctrl+O` | 切換展開所有工具結果（折疊/展開較長的 bash 與 read 輸出） |
| `Ctrl+Y` | 開啟 **yank 模式** — 選取區塊並複製其乾淨內容 |
| `Ctrl+F` | 開啟 **對話紀錄搜尋** — 輸入查詢字串，`Enter`/`n` 循環跳轉 |
| `Shift+Tab` | 循環切換 **權限模式** — `default → accept_edits → plan → bypass → …` |
| `PgUp` / `PgDown` / `Home` / `End` | 捲動對話紀錄 |
| 滑鼠滾輪 | 捲動對話紀錄 |

---

## 4. Yank 模式 — 從對話紀錄複製

對話紀錄中的每個區塊都會在左側繪製時間軸裝飾線（`│`、`├─` 等），讓對話以結構化方式呈現。缺點是：一般終端機的拖曳選取會複製畫面上所有可見內容——包含這些裝飾符號。貼到其他視窗後會得到像這樣的結果：

```
▶ who are you?
│
│ I'm evva — an interactive coding assistant…
│
```

要複製不含裝飾的乾淨內容，evva 內建了 **yank 模式**，能夠辨識區塊邊界。這是標準的乾淨複製途徑；在終端機不完整支援剪貼簿逸出序列時，也是唯一可用的方式。

**使用 `Ctrl+Y` 開啟。** 一次只會在一個區塊上顯示青色粗體的邊欄提示；狀態列上方的提示文字會顯示當前游標位置（`yank 3/5`）與按鍵對照。

| 按鍵 | 效果 |
| --- | --- |
| `j` / `↓` | 下一個區塊（較新） |
| `k` / `↑` | 上一個區塊（較舊） |
| `g` | 跳到第一個區塊 |
| `G` | 跳到最後一個區塊 |
| `Enter` / `c` | 將聚焦區塊的乾淨文字複製到系統剪貼簿 |
| `e` | 僅切換此區塊的展開/折疊（在複製長工具輸出前很實用） |
| `q` / `Esc` | 離開 yank 模式（清除邊欄提示） |
| `Ctrl+C` | 離開 + 退出 evva |

**複製了什麼。** 每個區塊提供一個 `PlainText()` 視圖，會移除 ANSI 控制碼與裝飾符號。使用者提示區塊對應提示文字，助手文字區塊對應 markdown 原始碼（非渲染後輸出），工具區塊則為呼叫標頭（`◢ name(...)`）加上結果內文。成功時狀態列會閃爍 `copied N chars`。

**技術細節 — OSC52。** Yank 模式使用 [OSC52](https://wezfurlong.org/wezterm/escape-sequences.html#operating-system-command-sequences) 終端機逸出序列將內容寫入剪貼簿。不需外部函式庫，也不依賴 `pbcopy`。終端機會將逸出序列轉發至作業系統剪貼簿。

| 終端機 | 是否預設可用？ |
| --- | --- |
| **iTerm2** | 是（預設） |
| **kitty** | 是 |
| **WezTerm** | 是 |
| **Alacritty** | 是 |
| **Ghostty** | 是 |
| **Apple Terminal.app** | 預設不可用 — 需啟用 `編輯 → 允許剪貼簿存取` 或更換終端機 |
| **tmux** | 需設定 `set -g set-clipboard on` |
| **GNU screen** | 大多無法使用；請改用 `Ctrl+Y` 從宿主終端機操作 |

若寫入失敗（內容超過 100 KB、終端機阻擋），狀態列會顯示 `clipboard: <error>`，yank 模式保持開啟，讓你可以嘗試其他區塊。

**為什麼不用原生拖曳選取？** evva 啟用滑鼠捕捉是為了讓滾輪能夠捲動對話紀錄。這項取捨使得拖放複製無法以原生方式運作——即使現代終端機支援 `Shift`/`Alt`+拖曳的繞過機制，選取結果仍然包含渲染後的裝飾符號（因為它們本就是畫在螢幕上的內容）。Yank 模式是將乾淨內容從程式內帶出的正式流程。

---

## 5. 對話紀錄搜尋

按下 `Ctrl+F` 開啟搜尋列。輸入查詢字串後按 `Enter` 跳到第一個匹配項。按 `n` 向前循環匹配項，或按 `N`（Shift+n）向後循環。按 `Esc` 關閉搜尋列。

---

## 6. 權限系統

### 權限模式

evva 透過**權限模式**對每個工具呼叫進行把關。共有四種模式，使用 `Shift+Tab` 循環切換：

| 模式 | 不需詢問即自動允許 | 適合情境 |
| --- | --- | --- |
| **`default`** | 唯讀工具（`read`、`tree`、`grep`、`glob`、`web_*`、`json_query`、`calc`、`daemon_list`、`daemon_output`）、代理自協調工具（`agent`、`todo_write`、`skill`、`tool_search`、`ask_user_question`），以及**唯讀 bash 指令**（`ls`、`cat`、`head`、`grep`、`git status`、`git log`、…）。檔案寫入與其他 bash 指令**會詢問**。 | 初學者、敏感工作、預設姿態 |
| **`accept_edits`** | 同 `default` + 檔案編輯（`edit`、`write`、`notebook_edit`）+ 常見檔案系統 bash 指令（`mkdir`、`touch`、`mv`、`cp`、`rmdir`、`ln`、`chmod`、`chown`）。 | 審閱中的程式碼迭代 |
| **`plan`** | 與 `default` 相同的唯讀安全清單。清單外的任何操作**直接拒絕**（不顯示提示）。 | 在決定修改前先探索程式碼庫 |
| **`bypass`** | 全部允許。危險指令分類仍會在背景記錄，但絕不阻擋。 | **僅限隔離容器與虛擬機使用** — 會傳遞至子代理 |

當前模式在狀態列中以彩色標籤顯示（`⛨ plan`、`⛨ bypass`、…）。`default` 會折疊此欄位以保持介面簡潔。

**以指定模式啟動：**

```bash
evva -permission-mode=plan                # 最安全：先調查
evva -permission-mode=accept_edits        # 自動套用編輯 + 安全的檔案系統指令
evva -permission-mode=bypass              # 無提示；僅限沙箱環境
```

CLI 參數優先；持久性預設值可寫入 `evva-config.yml`：

```yaml
permission_mode: default     # default | accept_edits | plan | bypass
```

### 計畫模式（`enter_plan_mode` / `exit_plan_mode`）

計畫模式是 `permission_mode: plan` 搭配兩個模型可呼叫的工具自動化整個流程。模型在處理非平凡任務（新功能、架構決策、跨多檔重構）時可自行切入計畫模式；你也能透過 `Shift+Tab` 手動進入。

**完整流程：**

1. **進入** — 模型呼叫 `enter_plan_mode`（或你用 `Shift+Tab` 切到 `plan`）。狀態列顯示 `⛨ plan`。除了一個專用的計畫檔之外，所有寫入都會被拒絕。
2. **計畫檔** — `<workdir>/.evva/plans/current.md`。每個 session 一份。`enter_plan_mode` 會建立或清空此檔；模型用一般的 `write` / `edit` 將計畫以 markdown 寫入此處。權限把關僅對這個確切路徑開放；其他任何寫入目標仍會被硬性拒絕，並顯示 *「plan mode forbids writes — Shift+Tab to exit plan mode」*。
3. **探索** — `read`、`grep`、`glob`、`tree`、`agent`（派生 `explore` 子代理）全部自動允許。模型藉此調查程式碼庫、起草計畫並反覆修改。
4. **退出** — 計畫完成後，模型呼叫 `exit_plan_mode`。evva 從磁碟讀取計畫檔並彈出 **Plan Approval** 覆蓋層，將 markdown 內容顯示出來：

```
┌─ PLAN APPROVAL ────────────────────────────────────┐
│ tool: exit_plan_mode                               │
│ mode: plan                                         │
│ reason: Plan approval — review and approve to exit │
│                                                    │
│ plan:                                              │
│   # Phase 7 — Plan Mode                            │
│   ## Context                                       │
│   …                                                │
│   ## Design                                        │
│   …                                                │
│                                                    │
│ ▶ [1] Allow once     (核准計畫並退出模式)          │
│   [2] Allow for…     (計畫場景幾乎用不到)          │
│   [3] Deny           (退回 — 模型會迭代)           │
└────────────────────────────────────────────────────┘
```

- **核准**（`1` / Enter）— 退出計畫模式，還原為先前的模式（`default` / `accept_edits` / 進入 `enter_plan_mode` 前的任何模式），模型開始實作。
- **拒絕**（`3` / Esc）— 鍵入一行原因；模型會收到 `"User requested changes: <原因>"`，留在計畫模式繼續修改計畫檔。

**注意事項：**

- 系統提示已告知模型：`exit_plan_mode` 就是核准信號，絕不能用 `ask_user_question` 問「這個計畫可以嗎？」。
- 子代理無法翻轉父 session 的計畫模式 — `enter_plan_mode` / `exit_plan_mode` 僅限根代理使用。
- 計畫檔在退出後仍會保留；下一次 `enter_plan_mode` 會清空它。若想保留某份計畫，請在重新進入計畫模式前先把 `current.md` 複製出 `.evva/plans/`。

### 工作樹（`enter_worktree` / `exit_worktree`）

工作樹（worktree）是同一個 git 倉庫在另一個分支上的平行 checkout，存放於獨立目錄。在你想要一個隔離的沙箱時使用：高風險的重構、會破壞性的實驗、想隨時可以丟棄的平行 feature 分支。

模型**只**會在你明確說出「worktree」時才呼叫這對工具 — 像是「開個 worktree」、「在叫 demo 的 worktree 裡做」、「離開 worktree」。比較模糊的說法（「切個分支」、「重構這段」）會讓 session 留在原本的 workdir。

**工作流程：**

1. **進入** — 模型呼叫 `enter_worktree`（可選擇傳入 `name`）。evva 執行 `git worktree add -b worktree-<slug> <repo>/.evva/worktrees/<slug>/ HEAD` 並將 session 的工作目錄切到新工作樹。之後的 `read` / `edit` / `write` / `bash` 都在工作樹中執行 — 原本目錄完全不會被動到。
2. **工作** — 正常驅動 session。讀檔、編輯、提交都發生在工作樹的獨立分支上。
3. **退出** — 完成後，模型呼叫 `exit_worktree`，搭配 `action: "keep"`、`"remove"` 或 `"merge"`：
   - `"keep"` — 工作樹目錄與分支留在磁碟上。想之後回來繼續或合併時用這個。
   - `"remove"` — 執行 `git worktree remove --force` 並刪除分支。若工作樹中有未提交的變更，除非你明確說「移除並丟棄變更」（模型會以 `discard_changes: true` 重新呼叫），否則工具會拒絕。
   - `"merge"` — 將工作樹分支整合回基底分支（`git merge --no-ff`），成功後移除該工作樹。worker 必須先提交。無衝突的合併會回報整合了多少 commit 與檔案並拆除工作樹；發生衝突時則**中止**（`git merge --abort`）並回報衝突的路徑，工作樹原封不動 — 絕不會留下合併到一半的狀態。
4. Session 還原至原始目錄；EVVA.md 與系統提示會以原 workdir 重建。

**子代理隔離** — `agent` 工具接受 `isolation: "worktree"`。以該旗標 spawn 一個子代理時，會在 `.evva/worktrees/agent-<id>/` 下建立屬於該子代理的工作樹，子代理整個生命週期都跑在裡面。若乾淨退出（沒有檔案變動、沒有新提交），evva 會自動移除工作樹；否則保留在磁碟，子代理結果裡會回報 `worktree_path:` / `worktree_branch:`。用 `worktree_list` 檢視所有這類工作樹，再用 `exit_worktree` 的 `action: "merge"` 整合好的那些 — 見下方「平行執行工作」。

**平行執行工作（fan-out → 檢視 → 整合）** — 平行工作的「整合」那一半：

1. **散開（fan out）** — 以 `isolation: "worktree"` spawn 多個子代理，各自負責任務的一個切片（例如各做一個檔案或模組）。它們並行執行，各自在 `.evva/worktrees/` 下的獨立分支上。
2. **檢視** — 它們完成後，呼叫 `worktree_list`。它會為每個存活的工作樹印出一列：分支、基底分支、領先/落後基底幾個 commit、是否有未提交變更，以及（若仍有子代理在寫入）擁有它的 daemon id（讓你分得清已完成與進行中的工作）。唯讀且自動允許。
3. **整合** — 用 `exit_worktree` 的 `action: "merge"` **逐一**整合好的分支，傳入 `worktree_list` 顯示的 `branch`。請循序合併、不要一次批次處理：每次合併都會推進基底，下一次會以新的基底重新檢查。衝突會乾淨地中止並指出衝突路徑 — 帶著衝突脈絡重新 spawn 那個 worker、先合併另一個分支，或手動解決。

這**不是** swarm。Fan-out 是短暫的工班 — 工作樹用完即丟、不持久化、工作合併後即解散。Swarm（`evva swarm`）則是有信箱與儲存的常設團隊。Fan-out 用於「把這件事拆到 N 個檔案、再整合」；swarm 用於持續性的任務。

**注意事項：**

- 工作樹位於 `<repo>/.evva/worktrees/<slug>/`。如果還沒把 `.evva/` 加進 `.gitignore`，建議加上。
- 計畫模式會拒絕 `enter_worktree` / `exit_worktree`（它們不在唯讀白名單裡）。需要新建工作樹時請先退出計畫模式。`worktree_list` 是唯讀的，因此到處都允許；`merge` 動作會更動基底分支，因此除非在 `bypass`，否則會像一般寫入一樣提示。
- 子代理無法在中途自行進入工作樹 — 僅根代理可以呼叫工具對。AgentTool 的 `isolation` 參數才是讓子代理跑在工作樹裡的官方做法。`worktree_list` 與 `merge` 動作同樣僅限根代理 — 子代理負責執行工作，由 lead 檢視並整合。
- v1 沒有 `.worktreeinclude` 支援 — 被 gitignore 的檔案（`.env`、本機設定）不會自動複製到新工作樹。需要時請在工作樹裡手動建立。

### 核准提示

在 `default` / `accept_edits` / `plan` 模式下，任何需要核准的操作都會彈出模態對話框：

```
┌─ APPROVAL ─────────────────────────────────────────┐
│ tool: bash                                         │
│ mode: default  risk: dangerous (sudo)              │
│ reason: matches dangerous prefix                   │
│                                                    │
│ input: sudo rm /tmp/evil-file                      │
│                                                    │
│ ▶ [1] Allow once                                   │
│   [2] Allow for this session                       │
│   [3] Deny                                         │
│                                                    │
│ [↑↓] choose · [Enter] confirm · [Esc] deny         │
└────────────────────────────────────────────────────┘
```

| 按鍵 | 效果 |
| --- | --- |
| `↑` / `↓` | 在按鈕間移動 |
| `1` / `a` | 允許一次 — 僅執行本次呼叫 |
| `2` / `s` | 允許此工作階段 — 同時新增記憶體規則，後續類似呼叫不再提示 |
| `3` / `d` | 拒絕 — 再按 Enter 可輸入提供給模型的拒絕原因 |
| `Enter` | 確認高亮選項（或送出拒絕原因） |
| `Esc` | 等同拒絕 |
| `Ctrl+C` | 拒絕 + 退出 |

**「允許此工作階段」** 會根據呼叫內容選擇合適的規則形式：對 `bash` 儲存第一個 token（因此核准 `git status` 後，後續 `git …` 呼叫都會放行，而非任意指令）；對 `read`/`write`/`edit` 儲存檔案路徑；其他工具則為工具層級的放行。工作階段規則在退出後消失；若要持久化，請手動編輯 `permissions.json`。

平行核准（代理在同一回合發出兩個 `bash` 呼叫）會堆疊 — 處理完最上層後，下一個會自動浮現。

### 權限規則

規則讓核准持久化，跨執行不會重複看到相同提示。有兩個作用範圍：

- `<workdir>/.evva/permissions.json` — **專案級**：跟隨 repo，可透過 git 分享
- `~/.evva/permissions.json` — **使用者級**：在所有工作目錄生效

格式：

```json
{
  "permissions": {
    "allow": [
      "bash(git:*)",
      "bash(npm:*)",
      "read(src/**)",
      "edit",
      "tree"
    ],
    "deny": [
      "bash(sudo:*)",
      "bash(rm -rf /)"
    ],
    "ask": [
      "bash(npm publish)"
    ]
  }
}
```

**規則語法**：`ToolName` 匹配該工具的所有呼叫。`ToolName(content)` 加入內容匹配：

| 工具 | 內容語法 | 範例 |
| --- | --- | --- |
| `bash` | `prefix:*`、`pattern *`、`git *` 或精確指令 | `bash(git:*)`、`bash(npm install *)`、`bash(make build)` |
| `read`、`write`、`edit`、`notebook_edit` | 針對 `file_path` 的 doublestar glob | `read(src/**)`、`write(./tmp/*.txt)`、`edit(**/*.go)` |
| 其他 | 對原始輸入的精確字串比對 | 少用；建議使用工具層級規則 |

**優先順序：**

1. `bypass` 模式 — 一律允許，忽略規則。
2. **deny 規則** — 最先檢查，在所有非 bypass 模式中優先於 allow。
3. **ask 規則** — 強制顯示提示，即使有更廣泛的 allow（或模式安全清單）匹配。
4. `plan` 模式 + 工具不在唯讀安全清單 → **拒絕**（無提示）。
5. 唯讀 / 自協調安全清單 → 允許。
6. Bash + 分類器判定為唯讀（`ls`、`cat`、`git status`、…）→ 允許。
7. 僅 `accept_edits`：`edit`/`write`/`notebook_edit` → 允許；bash 常見檔案系統指令（`mkdir`/`mv`/`cp`/…）→ 允許。
8. **allow 規則** — 匹配 → 執行。
9. 最終回退 — 詢問。

各行為（deny/ask/allow）內的來源優先順序為 `session > project > user`，因此工作階段的「允許此工作階段」會覆蓋使用者範圍規則，但永遠不會覆蓋 deny。

### 密鑰遮蔽（Secret Redaction）— 什麼可以「離開」

權限決定**什麼可以執行**，遮蔽決定**什麼可以離開**。兩者是不同的軸線，也無法互相取代：讀取 `config/production.yml` 可能是完全正當的工具呼叫，但你未必希望它的**結果**被送到模型供應商。

工具回傳的所有內容都會被附加到對話中、送往該工作階段所使用的供應商，並寫入磁碟上的工作階段快照。遮蔽會先掃描這些輸出中的密鑰特徵，並以佔位符取代：

```
$ bash: cat .env
AWS_ACCESS_KEY_ID=[REDACTED:aws-access-key:9c31]
GITHUB_TOKEN=[REDACTED:github-token:4f2a]
DATABASE_URL=postgres://app:[REDACTED:url-credentials:7b0e]@db.internal:5432/prod
LOG_LEVEL=info
```

請注意保留下來的部分：變數名稱、主機、連接埠、資料庫名稱，以及所有非密鑰的行。模型仍然能完成工作——它只是永遠不會知道那些值。

**佔位符是穩定的。** 同一個密鑰永遠產生同一個 token，因此模型仍可推理「這把金鑰同時出現在兩個檔案裡」，卻從未看過它。兩把不同的金鑰會得到兩個不同的 token。那四位十六進位數是該值的**不可逆**指紋，而非它的前綴。

**預設開啟。** 這是 evva 中唯一預設「選擇退出（opt-out）」的設定。以 `redaction: false` 關閉，將回復逐位元組原樣傳遞。

**會被攔截的：**

| | |
| --- | --- |
| 雲端 | AWS access/secret key、Google API key、GCP 服務帳戶金鑰 id |
| 程式碼託管 | GitHub PAT（傳統與細粒度）、GitLab token |
| SaaS | Slack token 與 webhook URL、Stripe key、OpenAI / Anthropic key、npm token |
| 結構化格式 | PEM 私鑰區塊、JWT、內嵌於 URL 的密碼 |
| 通用規則 | **名稱**本身即宣告為機密的變數其值（`*_SECRET`、`*_PASSWORD`、`*_TOKEN`、`*_API_KEY`、`*_ACCESS_KEY`、`*_PRIVATE_KEY`、`*CREDENTIAL*`） |
| 熵值 | 位於賦值或引號字串中的高隨機性值——這是針對沒有公開格式的密鑰所設的後備網 |

**不會被攔截的**——之所以明說，是因為一個誇大自身能力的安全功能比沒有更糟：

- **操作者輸入永不遮蔽。** 你自己輸入或貼上的內容是你的；遮蔽它會破壞「幫我輪替這把金鑰」這類流程，而且只是看起來像保護——你隨時可以再貼一次。
- **十六進位編碼的密鑰**會刻意穿過熵值後備網。其門檻刻意設在十六進位所能達到的上限之上，讓 lockfile 雜湊、git SHA 與 UUID 在結構上免疫——一個會弄壞日常工作的遮蔽器會被關掉，然後它就什麼都保護不了。這類值通常仍會被通用的**名稱**規則攔截。
- **看起來像一般文字的密鑰**無法用格式或熵值偵測，不會被攔截。
- **不檢查影像位元組。** 這個機制讀的是文字。
- 它**降低暴露，而非消除暴露**。是安全帶，不是保險庫。

**`/redactions`** 顯示本次工作階段被遮蔽的內容——佔位符、命中的規則、以及該值的長度。按 `r` 顯示真實值。該面板僅在 UI 端呈現，顯示原值永遠不會把它放回對話中。當某條規則誤判了非密鑰的內容時，用它來確認該把什麼加入允許清單。

遮蔽同樣涵蓋子代理與 swarm 成員——它們跑的是同一套代理迴圈，而且因為佔位符由內容推導而來，同一個密鑰在任何地方被看到都會得到同一個 token。

調整方式：

```yaml
redaction: true               # 預設開啟；false = 逐位元組原樣傳遞
redaction_allow:              # 正規表示式；匹配到的值永不遮蔽
  - "^AKIAIOSFODNN7EXAMPLE$"  # 測試資料中已公開的範例金鑰
redaction_disable:            # 要完全關閉的規則 id
  - high-entropy              # 保留格式規則，關掉熵值後備網
```

`redaction_allow` 與 `redaction_disable` 僅能透過 YAML 設定（`/config` 只提供開關）。錯誤的正規表示式或不存在的規則 id 會在啟動時直接失敗，而不會靜默地停用某條規則——錯誤訊息會列出有效的 id。

### 沙箱化執行（Sandboxed Execution）— 指令在**哪裡**執行

權限決定什麼可以執行，遮蔽決定什麼可以離開，沙箱則決定**在哪裡執行**。這是第三條軸線，同樣無法取代前兩者：一個已核准的 `bash` 呼叫仍然是已核准的呼叫——沙箱只改變它是跑在你的機器上，還是跑在容器裡。

預設情況下，每個 `bash` 呼叫（主代理、子代理、swarm 成員）都是直接在主機上的子行程。它有逾時與 kill-tree，但沒有檔案系統隔離、也沒有網路邊界；`cd /` 是可行的，`curl | sh` 會真的裝到你的機器上。

啟用容器執行環境之後，子代理就能改用 `isolation: "sandbox"` 生成：

```yaml
sandbox_runtime: docker       # ""（關閉，預設）| docker | podman
sandbox_image: alpine:3.20    # 選填；否則讀 .devcontainer/devcontainer.json
sandbox_network: allow        # allow（預設）| none
```

**你會得到什麼。** 沙箱工作階段就是 worktree 工作階段**再加上**一個把該 worktree 掛載到 `/workspace` 的容器。編輯仍然落在主機上（`edit`/`write` 工具完全不變，所以你可以照常查看結果），但 shell 指令跑在容器裡。你檔案系統的其餘部分（`~/.ssh`、旁邊的其他 repo、worktree 以上的一切）根本不存在於容器中；搭配 `sandbox_network: none` 則完全沒有網路。

**你不會得到什麼。** worktree 本身在容器內可完整讀寫——這是刻意的，因為代理需要看到自己的建置產物。沙箱保護的是**主機不受這個工作階段影響**；worktree 隔離保護的是**repo 的其餘部分不受這個工作階段影響**。兩者互補，而 `"sandbox"` 會同時開啟。

**映像選擇**沿用你多半已經有的慣例：若 repo 內有 `.devcontainer/devcontainer.json`，就使用其中的 `image`；否則請設定 `sandbox_image`。兩者皆無時，沙箱生成會**明確失敗**——evva 絕不會靜默退回成不沙箱執行，因為那等於在你剛要求隔離的當下把隔離拿掉。（若 devcontainer 只用 Dockerfile 建置而未指定 image，此層級不處理，請將 `sandbox_image` 指向已建好的映像。）

每個工作階段只啟動一個容器並重複使用，因此容器啟動成本只付一次，而不是每次呼叫都付。首次執行可能還要拉取映像。工作階段結束時（無論成功或中止）容器都會被移除。

在 swarm 中，沙箱是**manifest 層級**的決定而非單次呼叫的參數——一個 worker 的信任層級屬於 roster 的一部分，而且會被 `member_spawn` 產生的臨時 clone 正確繼承：

```yaml
settings:
  sandbox: true         # 所有 worker 預設進沙箱
workers:
  - agent: coder        # 繼承 settings.sandbox
  - agent: docs-writer
    sandbox: "off"      # 這個留在主機上
```

Leader 一律豁免：沙箱蘊含 worktree，而 leader 必須待在 base checkout 上才能合併其他成員的分支。

`worktree_list` 會標示哪些 worktree 由容器支撐，`list_members` 會標示哪些成員在沙箱中，因此不必翻設定就能看見這條邊界。

> **命名說明。** `bash` 工具有一個舊參數 `dangerouslyDisableSandbox`。它是被接受但無作用的空操作，且指的是**權限閘門**而非本功能——evva 早期的用語把「sandbox」用在核准層。兩者是不相干的軸線。

---

## 7. 子代理與人格

根代理可以生成子代理。內建兩種：

- **`explore`** — 唯讀檢查。工具僅限 `read`、`grep`、`tree`、`glob`、`web_search`、`json_query`。模型在進行「X 定義在哪／哪些檔案參照 Y」這類查詢時使用，沒有變更風險。
- **`general-purpose`** — 具寫入能力。攜帶 fs + shell + web + util 工具集。

執行中的子代理會在輸入框上方以水平橫列晶片顯示。非同步子代理在背景完成——其摘要會在下一次迭代中以模擬使用者訊息的形式出現在頂端，對話會自動接收。架構嚴格為兩層：子代理無法再生成子代理。

**使用者自訂子代理。** 在 `~/.evva/` 底下放入 `agents/<name>/` 目錄（與 `/profile` 同樣的格式——見上文），並在 `meta.yml` 設定 `as: [subagent]`。從磁碟載入的代理會自動出現在 Agent 工具的 `subagent_type` 列舉中，無須重新編譯。

**跨人格委派。** 宣告為 `as: [main, subagent]` 的人格既可在 `/profile` 中選擇，也可從執行中的根代理呼叫。這就是讓內建 `evva` 能將財務問題委派給使用者自訂 `nono` 人格的機制——根代理呼叫 Agent 工具並指定 `subagent_type: "nono"`，spawner 以 `nono` 的系統提示詞與工具建立子代理、執行一次，再把摘要送回 evva 的對話紀錄。

你不需要手動驅動子代理；模型會自行決定何時生成。

### 動態工作流（選用）— 由引擎執行的任務圖

在 `/config` 將 `enable_dynamic_workflow` 切為 `true`（下次啟動生效）。這把 swarm 的動態工作流執行模型（v1.10）帶進單機 TUI：主代理不再使用 todo 清單，改用一塊**工作流看板**——由任務組成的依賴圖——加上一個行程內引擎，自動為看板派工子代理 worker。規劃一次，機器派工，判斷權留在模型手上。

- **任務圖。** 模型用 `wf_task_create` 拆解目標：*worker 任務*帶著生成規格（代理類型、平行寫檔用的 `worktree` 隔離、模型等級），依賴一完成就自動派工；*self 任務*是模型自己做的步驟，記在同一張圖上。依賴是對既有任務的 AND-join 且不可變——擴充圖的方式是新增任務。
- **驗收政策。** 預設 `verify: "leader"`：每個 worker 的結果都會送回模型判斷，通過後任務才完成、後續任務才解鎖。`verify: "auto"` 讓宣告為機械性的步驟**零打擾**地完成並串聯——最後只收到一則「workflow settled」摘要，紀錄都在看板上。worker 崩潰的任務無論政策為何都不會自動完成。
- **看板面板。** 輸入框上方會出現 `WORKFLOW` 面板：轉圈符號代表 worker 執行中，`⛓ #…` 標示被依賴擋住的任務，`auto` 是自動驗收，`ᵂ` 是 worker 任務。worker 同時出現在子代理晶片列與 `daemon_list`；可用 `daemon_stop` 停掉單一 worker，或 kill 掉 `local_workflow` daemon 暫停所有派工（看板工具照常可用；執行中的 worker 會做完並記錄結果）。
- **重啟安全。** 看板以工作階段為單位存在 `~/.evva/workflows/<session>.jsonl`，resume 時重放；隨行程一起死掉的 worker 任務會自動重新排隊、重新派工。`/clear` 開一塊全新看板。
- **旋鈕。** `workflow_max_workers`（預設 4）限制引擎同時派工的 worker 數；超出的就緒任務排隊等空位。

旗標關閉（預設）時一切不變——todo 清單、提示詞、工具與現況逐位元組相同。swarm 成員永遠不會掛載單機看板：swarm 有自己的 ledger（見 swarm 手冊的動態工作流章節）。

---

## 8. Hooks 鉤子

Hooks 是使用者自訂的 shell 指令或 HTTP webhook，在代理迴圈中的六個明確時機觸發。用途包括：工具呼叫前驗證、編輯後自動格式化、自訂日誌、阻擋已知不良指令，或在長時間執行的核准上把通知送到 Slack／桌面通知器。

### Hook 設定檔位置

兩個檔案，皆為選用，於啟動時合併：

- `<workdir>/.evva/settings.json` — **專案** hook。隨 repo 存在，可選擇透過 git 分享。
- `~/.evva/settings.json` — **使用者** hook。在每個工作目錄都會套用。

專案 hook 先觸發；專案 hook 回傳 `"continue": false` 會中斷該次觸發的使用者 hook。格式不正確的條目會在啟動時於 stderr 顯示為警告——其餘檔案內容仍會載入。

### 檔案格式

JSON 格式（與 Claude Code 的 `settings.json` `hooks` 區塊相容，兩邊的檔案可互通）：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          { "type": "command", "command": "/path/to/check.sh", "timeout": 30 }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "edit|write",
        "hooks": [
          { "type": "command", "command": "goimports -w \"$EVVA_TOOL_INPUT_PATH\"" }
        ]
      }
    ],
    "Notification": [
      {
        "hooks": [
          { "type": "http", "url": "https://hooks.slack.com/...", "method": "POST", "async": true }
        ]
      }
    ]
  }
}
```

**Matcher**：對工具名稱進行 doublestar glob 比對。空 matcher = 全部符合。支援交集（`bash|grep`）與萬用字元（`tool_*`）。不附帶工具名稱的事件（SessionStart、Stop、Notification）會忽略 matcher。

**Hook 條目欄位**：

| 欄位 | 適用 | 意義 |
| --- | --- | --- |
| `type` | 兩者 | `"command"`（shell 子行程）或 `"http"`（HTTP 請求） |
| `command` | command | shell 指令。stdin 為 JSON payload；stdout 為可選的 decision |
| `url` | http | 接收 payload 的 endpoint |
| `method` | http | HTTP method，預設 `POST` |
| `headers` | http | 選用的 headers 對應 |
| `timeout` | 兩者 | 秒（1–600）。預設視事件而定 |
| `async` | 兩者 | 觸發後不等待。command 預設 `false`，http 預設 `true` |

子行程 hook 的環境變數會包含 `EVVA_PROJECT_DIR`。

### 事件

| 事件 | 觸發時機 | 典型用途 |
| --- | --- | --- |
| `SessionStart` | 代理啟動一次 | 預熱快取、為第一個 prompt 注入額外脈絡 |
| `UserPromptSubmit` | 使用者 prompt 加入 session 之前 | prompt 驗證、機密遮蔽 |
| `PreToolUse` | 權限閘門執行之前 | 阻擋不良呼叫、改寫參數、覆蓋閘門 |
| `PostToolUse` | 工具回傳之後 | 自動格式化、保留日誌、為下一輪附加脈絡 |
| `Stop` | 主代理進入終止輪（沒有更多工具呼叫） | 摘要匯出、稽核日誌 |
| `Notification` | 迭代上限、內部錯誤、需要核准 | 在長時間核准時送 Slack 通知、桌面通知 |

### Payload 與 Decision

每個 hook 都會收到一個 JSON payload（command 從 stdin、webhook 從 HTTP body）。共同信封：

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "/abs/working/dir",
  "permission_mode": "default",
  "agent_id": "uuid",
  "agent_type": "main",
  "hook_event_name": "PreToolUse"
}
```

事件特有欄位：

- `SessionStart`：`source`（`"startup"`）、`model`
- `UserPromptSubmit`：`prompt`
- `PreToolUse`：`tool_name`、`tool_input`（模型送出的原始 JSON）、`tool_use_id`
- `PostToolUse`：`tool_name`、`tool_input`、`tool_use_id`、`tool_response`、`is_error`
- `Stop`：`last_assistant_message`、`stop_hook_active`
- `Notification`：`message`、`title`、`notification_type`

Command hook 可以把一個 JSON 物件寫到 stdout 來影響迴圈：

```json
{
  "continue": false,
  "decision": "block",
  "reason": "lint failed: see stderr",
  "systemMessage": "ran golint, found 3 issues",
  "hookSpecificOutput": {
    "permissionDecision": "deny",
    "permissionDecisionReason": "vendor directory is read-only",
    "additionalContext": "the next turn should retry the edit elsewhere",
    "updatedInput": { "file_path": "/safer/path.go" }
  }
}
```

各事件的效果：

- **PreToolUse**：`hookSpecificOutput.permissionDecision`（`"allow"` / `"deny"` / `"ask"`）覆蓋閘門。`updatedInput` 在閘門檢查前改寫工具參數。`decision: "block"` 或 `continue: false` 以給定的 `reason` 直接阻擋該呼叫。
- **PostToolUse**：`additionalContext` 會附加到 LLM 下一輪看到的工具結果。`block` / `continue` 會被忽略——post-tool hook 無法把已執行的工具收回。
- **UserPromptSubmit**：`additionalContext` 會附加到使用者 prompt。`block` / `continue: false` 會完全丟棄該 prompt。
- **Stop**：`block` / `continue: false` 會再次進入迴圈一次（`stop_hook_active` 旗標可防止無限重入）。
- **SessionStart**：`additionalContext` 與 `hookSpecificOutput.initialUserMessage` 會插入第一個使用者 prompt 的前面。
- **Notification**：stdout 會被忽略——純粹是側通道訊號。

stdout 為空（或非 JSON）的 hook 表示「沒有意見、直接通過」。Command hook 回傳結束碼 2 會被視為硬性阻擋，訊息從 stderr 讀取。

超過 `timeout` 的子行程會被強制終止，其 decision 也會被丟棄。HTTP hook 預設為非同步觸發即忘——失敗會記錄日誌但永遠不會阻擋迴圈。

---

## 9. MCP 伺服器

evva 可以消費任何 [Model Context Protocol](https://modelcontextprotocol.io) 伺服器（檔案系統、GitHub、Slack、Notion，或你自己的內部伺服器）所提供的工具與資源，完全不需為每個伺服器寫程式。已設定的伺服器會在啟動時連線，其工具會以 `mcp__<server>__<tool>` 的名稱出現在 evva 的延遲工具（deferred tool）目錄中，並在需要時透過 `tool_search` 載入。

### 設定伺服器

MCP 伺服器設定放在 **與 hooks 相同的 `settings.json` 檔案**裡的 `mcpServers` 區塊——所有 evva 擴充設定都集中於一處：

- **專案層級：** `<workdir>/.evva/settings.json`
- **使用者層級：** `~/.evva/settings.json`（`<APP_HOME>/settings.json`）

同名時，專案層級會覆蓋使用者層級。

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${HOME}/work"]
    },
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {"Authorization": "Bearer ${GITHUB_MCP_TOKEN}"}
    }
  }
}
```

各伺服器欄位：

| 欄位 | 適用於 | 說明 |
| --- | --- | --- |
| `type` | 兩者 | `"stdio"` 或 `"http"`。省略時由 `command`→stdio／`url`→http 自動推斷。 |
| `command`、`args`、`env` | stdio | 啟動子行程。`${VAR}` 與 `${VAR:-default}` 會在載入時從環境變數展開。 |
| `url`、`headers` | http | Streamable HTTP 傳輸（2025-03-26 規格）。header 值同樣會展開環境變數。 |
| `timeout` | 兩者 | 連線逾時秒數。預設 30，最大 600。 |
| `disabled` | 兩者 | `true` 會完全略過該伺服器——不啟動子行程、不發送請求。 |

設定錯誤的伺服器永遠不會阻擋啟動：失敗會記錄日誌（在[日誌](#12-日誌)中尋找 `mcp: connect` 行），該伺服器被略過，其餘伺服器照常連線。編輯 `settings.json` 會在下次啟動時生效（不支援熱重載）。

### 使用 MCP 工具

被發現的工具屬於**延遲工具**——它們的名稱會列在系統提示的 `<available-deferred-tools>` 區塊，但 schema 不會預先載入。evva 第一次需要時會用 `tool_search` 取得工具 schema，與其他延遲工具一樣。你不需要做任何特別的事：直接請 evva 完成任務，它會自行尋找並呼叫 MCP 工具。

MCP 工具呼叫與內建工具走相同的流程：

- **權限**（[§6](#6-權限系統)）：第一次呼叫未知的 MCP 工具會要求核准。若要永久允許，請以完整名稱新增規則——`mcp__filesystem__read_file`——或使用萬用字元如 `mcp__filesystem__*` / `mcp__*`。deny 規則會阻擋。
- **Hooks**（[§8](#8-hooks-鉤子)）：像 `mcp__**__write_*` 這樣的 `PreToolUse` matcher 會在符合的 MCP 呼叫前觸發，可阻擋、改寫輸入或覆寫權限決策——用一條規則就能稽核所有 MCP 寫入。
- **子代理**（[§7](#7-子代理與人格)）：子代理共用父代理的 MCP 連線，因此被委派的任務不需重新連線即可使用相同的伺服器。

### 資源（Resources）

部分 MCP 伺服器除了工具之外還會提供**資源**（檔案、紀錄、文件）。有兩個延遲工具可跨所有已連線伺服器運作：

- `list_mcp_resources`——列出可用資源（每筆都標記其來源 `server`）。可用 `server` 參數過濾單一伺服器。
- `read_mcp_resource`——以 `{server, uri}` 讀取單一資源。文字直接內嵌回傳；二進位內容會存到 `~/.evva/mcp-blobs/` 並回傳路徑（需要原始位元組時可用 `read` 工具讀回）。

### 需 OAuth 授權的伺服器

若 HTTP 伺服器在首次連線時回傳 `401 Unauthorized`，evva 會將它標記為 `needs-auth`，並提供一個一次性的 `mcp__<server>__authenticate` 工具（而非該伺服器的真正工具）。當 evva 呼叫它時，你會看到一個帶有授權 URL 的 `ask_user_question` 提示：

1. 在瀏覽器開啟該 URL 並完成登入。
2. 在提示中選擇 **「I'm done」**。

evva 接著會重新連線，該伺服器的真正工具會在本次工作階段內變為可用。（本版本的 token 僅保存在記憶體中——重啟 evva 後需重新授權。）

### 把 evva 當成 MCP 伺服器對外提供

以上都是 evva **對外呼叫**。`evva mcp-serve` 走的是反方向：把這個 evva 安裝本身開成一台 MCP 伺服器，讓任何 MCP 客戶端——Claude Desktop、IDE、編排器，或另一個 evva——反過來呼叫它。

可對外開放的有兩種，且**預設什麼都不開放**：

| 種類 | 呼叫方拿到什麼 | 工具名稱範例 |
| --- | --- | --- |
| `persona` | 一整個人格，端到端執行。一次呼叫 = 一個完整的 agent turn，回傳它的最終答案。 | `evva_explore` |
| `tool` | 單一 evva 工具，直接轉呼叫。 | `tree` |

#### 設定開放清單

`mcpServe` 區塊，與 `mcpServers` 放在相同的 `settings.json` 檔案裡：

```json
{
  "mcpServe": {
    "expose": [
      {"kind": "persona", "name": "evva"},
      {"kind": "tool", "name": "tree"}
    ],
    "timeout": 600
  }
}
```

| 欄位 | 說明 |
| --- | --- |
| `expose` | 開放清單。每一筆都在**啟動時**驗證——人格名稱打錯會讓伺服器直接起不來，並在錯誤訊息中列出實際可用的名稱。 |
| `timeout` | 單次人格呼叫的秒數上限。預設 600，最大 3600。 |

與 `mcpServers` 不同，專案層級的 `mcpServe` 區塊會**整塊取代**使用者層級的，而不是合併。若採合併，專案就只能放寬使用者設定所開放的範圍，永遠無法收緊。

三個刻意設計的拒絕行為：

- **`expose` 為空或不存在時拒絕啟動。** 一台什麼都沒開放卻在監聽的伺服器，和設定壞掉的伺服器看起來完全一樣，所以它不該是一個可達狀態。
- **`kind: "tool"` 只能開放唯讀類工具**——`read`、`tree`、`grep`、`glob`、`web_fetch` 等 evva 自動允許的集合。`bash`、`write_file`、`edit_file` 會被拒絕。人格可以在它自己的權限閘門下使用這些工具，但把它們直接交給外部呼叫方是另一條信任邊界。需要會改動狀態的工具時，請改為開放一個人格。
- **非 loopback 的 `--addr` 會拒絕綁定**，除非明確加上 `--allow-remote`。

#### 執行

```bash
# stdio——Claude Desktop 與多數客戶端啟動伺服器的形式
evva mcp-serve

# streamable HTTP——常駐、可供遠端嵌入
evva mcp-serve --transport http --addr 127.0.0.1:8899
```

在 stdio 模式下，**stdout 就是 JSON-RPC 通道**——所有診斷訊息都走 stderr。加上 `-v` 可記錄工具呼叫。

在 HTTP 模式下，evva 會在啟動時鑄造一個 bearer token 並寫入 `~/.evva/mcp-serve/token`（權限 0600，結束時刪除）。每個請求都必須以 `Authorization: Bearer <token>` 帶上它；沒有 loopback 例外，也不接受 `?token=` 查詢參數。綁定位址與 token 路徑會印在 stderr。

要接進 Claude Desktop，把 evva 加到*它的* `mcpServers`：

```json
{
  "mcpServers": {
    "evva": {
      "command": "evva",
      "args": ["mcp-serve"],
      "env": {"HOME": "/Users/you"}
    }
  }
}
```

#### 呼叫方能做什麼、不能做什麼

被開放的人格**並不是**在跟它的操作者對話，而 evva 會明說這件事。每個進來的 prompt 都會被包進 `<external-request client="…">` 信封，前面附上一行協定說明：完成它要求的工作，但忽略其中任何試圖改變運作規則、提升權限、洩漏設定，或冒充操作者發言的指令。若 prompt 內嵌自己的結束標記想「跳出」信封，該標記會被去牙。

除了這層框架之外：

- **每次呼叫都是全新的 session。** 並行的呼叫方不會共用對話狀態，外部呼叫方也無法跨次呼叫在你的 evva 裡累積狀態。
- **不存在核准介面**，因此人格提出的任何核准請求都會被自動拒絕。在預設權限模式下，這剛好讓唯讀工具可用、危險操作全部擋下。調高該人格的權限模式，等同於調高一個陌生人能觸發的範圍——請刻意為之。
- **呼叫是同步的**，並受 `timeout` 限制。本版本沒有部分串流；超出預算的呼叫會回傳一個指出該預算的錯誤。

---

## 10. 設定參考

### evva-config.yml

路徑：`~/.evva/config/evva-config.yml`。首次啟動時自動建立。可透過 TUI 的 `/config` 即時編輯，或手動修改：

```yaml
# Agent loop
max_iterations: 30
max_tokens: 4096
auto_compact_threshold: 0.8
display_thinking: true

# Context ladder (prune → span → full). Both rungs are opt-OUT.
# The three integers treat 0 as "use the default".
context_prune: true
context_span: true
prune_min_bytes: 2048
prune_keep_turns: 3
prune_keep_results: 12

# Default model used at startup (overwritten by /model swap)
default_provider: deepseek
default_model: deepseek-v4-pro

# Default thinking effort: low | medium | high | ultra. Overwritten by /effort.
default_effort: medium

# Default persona that boots — must match an agent name in the registry
# (built-in "evva" or a user-authored agent under ~/.evva/agents/<name>/).
# Overwritten by /profile. Empty falls back to "evva".
default_profile: evva

# Permission stance at startup. Cycle at runtime with Shift+Tab; -permission-mode CLI flag overrides.
permission_mode: default     # default | accept_edits | plan | bypass

# 密鑰遮蔽（LLM 出口邊界）— 見 §6「密鑰遮蔽」。
# 本檔案中唯一預設「開啟」的設定：缺少此鍵即為開啟。
redaction: true              # false = 工具結果逐位元組原樣傳遞
redaction_allow: []          # 正規表示式；匹配到的值永不遮蔽
redaction_disable: []        # 要關閉的規則 id（例如 "high-entropy"）

# 沙箱化執行 — 見 §6「沙箱化執行」。選擇加入：runtime 留空表示每個 bash
# 呼叫都照舊直接在主機上執行。
sandbox_runtime: ""          # ""（關閉）| docker | podman
sandbox_image: ""            # 留空 = 讀 .devcontainer/devcontainer.json；兩者皆無則拒絕沙箱化
sandbox_network: allow       # allow | none（容器內沒有網路）

# Web tooling
fetch_max_bytes: 100000
tavily_api_key: ""

# 記憶（位於 ~/.evva/memory/ 的型別化記憶目錄）
enable_auto_memory: true     # 記憶指引 + MEMORY.md 索引 + 寫入豁免 + 召回
enable_memory_recall: true   # 每回合相關性側查詢（成本開關；設為 false 只保留索引）
memory_recall_model: ""      # 留空 = 當前供應商中較便宜的模型（anthropic→sonnet、deepseek→flash、openai→gpt-5.4-mini、glm→glm-4.6 @ medium；ollama→當前模型+effort）
enable_auto_dream: false     # 背景「做夢」：閒置時整併/修剪/重建記憶索引（預設關閉；真實但罕見的 token 成本）
embedding_provider: ""       # "" = 關鍵字記憶搜尋（預設）| ollama（本機、私密）| openai（雲端 —— 會把記憶內容送出機器）
embedding_model: ""          # 留空 = 該後端的預設（ollama→nomic-embed-text，openai→text-embedding-3-small）
auto_dream_model: ""         # 留空 = 與召回相同的便宜 per-provider 預設（auto_dream_min_hours: 24、auto_dream_min_sessions: 5 可調整閘門）

# 倉庫地圖（在工作階段開始時注入的 LSP 程式碼庫概覽；僅主代理）
enable_repo_map: false       # 選用；關閉時提示詞與現況逐位元組相同，零 LSP 呼叫
repo_map_token_budget: 2000  # 限制地圖大小；超出時優先丟棄排名較低的符號

# 動態工作流（單機任務圖看板 + 引擎派工的子代理 worker；僅主代理）
enable_dynamic_workflow: false  # 選用；以 wf_task_* 看板 + 自動派工引擎取代 todo_write
workflow_max_workers: 4         # 引擎同時派工的 worker 上限；≤0 重設為 4

# 自我修復編輯——見下方「自我修復編輯」。核心同步（didChange，讓下一輪的
# 診斷是真的）只要有設定 LSP 就會運作，與此旗標無關；此旗標只控制「同步」層。
lsp_diagnostics_on_edit: false  # 選用；在 edit/write 後加入一段有上限的等待（約 750ms）

# 疊加在主人格聲音上的輸出風格 — default | Explanatory | Learning |
# 自訂 output-styles/*.md 名稱。/output-style 會覆寫此值（立即生效）；
# 直接改這裡則在下次 profile 重建時生效。
output_style: default

# Per-provider credentials. Empty api_url falls back to the constant's default.
# glm（Zhipu/z.ai）走 Anthropic 相容端點;讀取圖片會以 image block 餵給 GLM,
# 但要「看懂」圖片需選用具備 vision 能力的 GLM 模型。模型:glm-4.6(一般)、glm-5.2(大型,~1M ctx)。
providers:
  anthropic: { api_key: "", api_url: "" }
  deepseek:  { api_key: "", api_url: "" }
  openai:    { api_key: "", api_url: "" }
  glm:       { api_key: "", api_url: "" }
  qwen:      { api_key: "", api_url: "" }   # 阿里雲 DashScope（OpenAI 相容）；api_url 預設國際/新加坡閘道，北京/美國請覆寫。模型：qwen3.7-plus（便宜）/ qwen3.7-max（旗艦，~1M ctx）
  ollama:    { api_url: "" }
```

### 記憶

evva 在 `~/.evva/memory/` 維護單一全域、以檔案為基礎的記憶。每則記憶是一個帶有
`name` / `description` / `type` frontmatter 的 Markdown 檔（四種型別為 `user`、
`feedback`、`project`、`reference`），目錄中的 `MEMORY.md` 為其索引。代理會用它
平常的檔案工具自行寫入與更新這些檔案；寫入限定在記憶目錄內者會自動核准，因此不會
為每則筆記提示你。

- **永遠載入的內容**：僅 `MEMORY.md` 索引（一份目錄），讓提示詞保持精簡。
- **相關性召回**：每回合開始時，一次廉價的側查詢會拉入與你訊息相關的少數記憶，
  它們會以 `<system-reminder>` 出現在對話紀錄／日誌中。設定
  `enable_memory_recall: false` 可只保留索引而略過此額外呼叫。預設使用當前供應商中
  較便宜的模型——Anthropic → Sonnet、DeepSeek → v4-flash、OpenAI → gpt-5.4-mini
  （皆為 medium effort）；Ollama 則沿用當前模型與 effort——亦可用 `memory_recall_model` 指定特定模型。
- **主動搜尋（`memory_search`）**：召回是**推**的 —— evva 在你的回合開始時決定要浮出什麼。`memory_search` 則是**拉**：當它在任務進行到一半才意識到需要某個沒被給到的東西（「我們之前決定 PR 要怎麼開？」），它可以自己去找。你不會直接呼叫這個工具，它是給代理用的。
- **語意搜尋（opt-in）**：設定 `embedding_provider` 之後，記憶內容會被建成向量索引，搜尋改為依語意比對 —— 查「部署流程」也能找到標題是「CI release flow」的記憶。沒設定時搜尋仍可用，只是退回關鍵字比對，而且結果會註明這件事，讓 evva 知道可能存在一則措辭不同、它沒找到的筆記。

  ```yaml
  embedding_provider: ollama      # ""（關閉，預設）| ollama | openai
  embedding_model: ""             # "" = 該後端的預設模型
  ```

  **這是記憶相關設定中唯一預設關閉的，而且是刻意的。** 打開它意味著要嘛花錢呼叫 API，要嘛 —— 選 `openai` 的話 —— **把你的記憶內容送給第三方**。`ollama` 全部留在本機且不需要 API key：`ollama pull nomic-embed-text` 然後設 `embedding_provider: ollama` 即可。

  向量快取放在 `~/.evva/memory/.index/`。它會增量重建（只處理有變動的部分）、在做夢之後刷新，而且是可拋棄的 —— 刪掉它會自己重建。第一次的冷啟建索引在背景進行，所以不會卡住啟動；在它完成之前的搜尋會走關鍵字模式。
- **來源標記（provenance）**：新寫入的記憶會記錄一行 `origin`，標明它是在哪個專案寫下的。記憶庫是跨專案共用的 —— 這正是重點，在某個 repo 學到的教訓在另一個 repo 也拿得到 —— 而 origin 就是讓後續工作階段能分辨「**這個**專案的建置慣例」和「別的專案的慣例」的依據。v1.18 之前寫的記憶沒有 origin，會被視為「來源不明」。
- **新鮮度**：召回時超過一天的記憶會在前面附上其年齡與「在當作事實前先對照現有
  程式碼驗證」的提醒。
- **背景整併（「做夢」）**：設定 `enable_auto_dream: true`（預設關閉）後，當你閒置
  且累積足夠的新工作階段時（預設：每 24 小時最多一次、且距上次後有 5 個工作階段——
  可用 `auto_dream_min_hours` / `auto_dream_min_sessions` 調整），evva 會 fork 一個
  受柵欄保護的背景代理來整理記憶：合併近似重複、修剪過時或被推翻的條目、並讓
  `MEMORY.md` 保持精簡。它被限制在記憶目錄內（沒有 shell，寫到別處會被拒絕），且同
  時只跑一個。可視為手動 `/remember` 審閱的自動補充。
- **關閉方式**：`enable_auto_memory: false`（或 `EVVA_AUTO_MEMORY=0`）會停用整個
  子系統——不建立目錄、不召回、不做夢、提示詞中也沒有記憶區段。

> 舊的雙檔模型（`USER_PROFILE.md` + 各專案的 `projects/<key>/MEMORY.md`）以及
> `update_user_profile` / `update_project_memory` 工具已移除。舊檔仍保留在磁碟上但
> 不再讀取——若有值得保留的內容，請複製到新的記憶中。

### 倉庫地圖（Repo map）

冷啟動時 evva 對你的程式碼庫結構一無所知，因此最初幾輪都耗在重新摸索東西放在哪裡。
設定 `enable_repo_map: true`（選用，預設關閉）後，evva 會在開場時附上一張精簡、
經過排名且受 token 預算限制的**倉庫地圖**——逐套件列出主要型別、函式及其簽章，注入
主代理的提示詞，讓它一開始就對結構有掌握。

- **來源**：地圖由你設定的語言伺服器建構（與 `lsp_request` 背後相同的
  `workspace/symbol` + `documentSymbol` 查詢，見 [lsp.md](lsp.md)）——不新增任何相依、
  不使用 tree-sitter。當倉庫的語言**沒有**設定語言伺服器時，會退化為以 grep 推導的
  粗略大綱（依目錄列出頂層宣告）並明確說明，而非什麼都不輸出。
- **排名與預算**：符號依種類排序（型別 → 函式 → 方法），讓 `repo_map_token_budget`
  （預設約 2000 tokens）優先花在關鍵符號上；超出預算時以 `… +N more` 標記丟棄排名較低
  的符號，絕不會在符號中途截斷。
- **放大 — `repo_map` 工具**：工作階段進行中，代理可呼叫
  `repo_map({path: "internal/agent"})` 取得某子樹更高細節的視圖（`detail: "full"`
  會納入成員），比開場概覽更深入。
- **僅主代理**：子代理（Explore/Plan/General）為各自的窄任務冷啟動，永遠不會收到地圖。
- **快照而非即時**：地圖只在工作階段開始時拍一次（並於 `/profile` 或 `/model` 切換時
  重建），不會隨你編輯而自動更新——代理會在工作中讀取檔案，而 `repo_map` 即是針對你
  正在改動的子樹的手動重讀。
- **關閉時的成本**：`enable_repo_map: false` 時主提示詞與從未有此功能的建置逐位元組
  相同，且**零** LSP 呼叫。地圖建構也受時間上限保護，因此冷啟動的語言伺服器索引絕不會
  卡住工作階段開始——附帶 `(indexing — partial)` 註記的部分地圖優於卡死的提示詞。

### 自我修復編輯

只要有設定語言伺服器（見 [lsp.md](lsp.md)），`edit` / `write` 現在都會通知它每一次
異動，因此它發布的診斷是針對代理**剛剛真正寫下的內容**——不會是過期的，也不會因為
伺服器沒有自行監看檔案系統而悄悄漏掉。

- **核心層（只要有設定 LSP 就會運作）**：edit 或 write 成功後，工具會送出
  `didOpen`（第一次接觸）或完整同步的 `didChange`（之後每次），並先清掉該檔案的過期
  診斷。伺服器重新分析後，產生的診斷會在模型的**下一輪**以 `<system-reminder>`
  送達——與倉庫地圖等功能相同的既有遞送路徑，只是現在可靠地餵給它內容，而非仰賴伺服器
  自行注意到變化。
- **同步層（選用，透過 `lsp_diagnostics_on_edit: true` 開啟）**：edit/write 這次
  工具呼叫本身會等待一段有上限的短暫時間（約 750ms），等候該檔案的診斷，並直接把它們
  併入同一次工具結果——讓模型能在**同一輪**就看到並修正自己的編譯／型別錯誤，不必等到
  下一輪。若時間內沒有任何診斷抵達，呼叫會回傳正常摘要（核心層仍會在下一輪照常送達）。
- **關閉時的成本**：沒有設定 LSP manager 時，兩層都是無操作——edit/write 的熱路徑不受
  影響。有設定 LSP 但 `lsp_diagnostics_on_edit: false`（預設）時，核心層的 `didChange`
  是非同步派送，不會為工具呼叫增加任何延遲。
- **適用範圍**：跟著工具走，不是跟著人格走——任何在有 LSP manager 的情況下執行
  `edit`/`write` 的代理（主代理或子代理）都會拿到這個功能。只涵蓋透過 `fs` 的
  `edit`/`write` 工具所做的檔案異動；`bash` 造成的變更（如 `sed`、`go fmt`）是已知、
  已記載的限制範圍之外（與 checkpoint/rewind 相同的限制）。

### .env（選用）

放置於工作目錄或 `~/.evva/.env`。僅用於部署 / 日誌控制——絕非使用者偏好設定：

```bash
APP_ENV=dev            # dev | prod
LOG_LEVEL=info         # debug | info | warn | error
LOG_FORMAT=text        # text | json
LOG_DIR=               # 未設定 → $EVVA_HOME/logs（預設）；填寫路徑 → 自訂目錄；明確設為空 → 改用 stdout
SKILLS_DIR=skills      # ~/.evva/ 下的子路徑
USER_PROFILE=user_profile.md
```

### CLI 參數

```bash
evva                                # 互動式 TUI（stdout 為 TTY 時預設）
evva -temp 0.7                      # 取樣溫度（預設不設定）
evva -max-tokens 2048               # 每次 completion 輸出上限（覆蓋 YAML）
evva -max-iters 40                  # 迴圈迭代上限（覆蓋 YAML）
evva -permission-mode=plan          # 以 plan 模式啟動（唯讀）
evva -permission-mode=bypass        # 以關閉權限閘門啟動
evva -no-tui "explain loop.go"      # 單次純文字模式
echo "list files in /tmp" | evva -no-tui   # 管線輸入提示
evva -no-tui -output-schema s.json "find bugs"  # headless 執行，最終答案輸出為 JSON
```

---

## 11. 執行模式 — TUI vs CLI

**互動式 TUI**（stdout 為 TTY 時預設）。包含對話紀錄、面板、狀態列等完整功能。

**純文字 CLI**（`-no-tui`，或 stdout 被管線重定向時）。單次流程：從參數/stdin 讀取提示 → 執行代理 → 以純文字串流事件 → 退出。CLI 模式沒有互動式核准介面——任何需要提示的呼叫都會**自動拒絕**，並提示可傳入 `-permission-mode=bypass` 或在 `permissions.json` 中新增規則。適用於腳本與 CI 環境。

**結構化輸出**（`-output-schema <file.json>`，僅限 CLI 模式）。提供一個
JSON-Schema 檔案，本次執行的最終答案就會以符合該 schema 的 JSON 回傳，而不是
散文：代理會拿到一個一次性的 `structured_output` 工具，其輸入 schema 就是你的
schema，模型一呼叫它執行就結束。事件軌跡改走 stderr，因此 **stdout 只承載一件
事——最終的 JSON**——直接接 `jq` 就能用：

```bash
cat > /tmp/verdict.json <<'EOF'
{"type":"object","required":["summary","ok"],
 "properties":{"summary":{"type":"string"},"ok":{"type":"boolean"}}}
EOF
evva -no-tui -permission-mode=bypass -output-schema /tmp/verdict.json \
  "run the test suite and report" | jq .ok
```

若模型最後以散文作答而沒有呼叫工具，evva 會以非零狀態碼退出並顯示
`run ended without structured output`，且 stdout 保持空白——腳本永遠不會把散文
誤當成契約結果。此旗標在 TUI 模式下會被忽略（附警告）。SDK 宿主可透過
`agent.WithStructuredOutput` 使用同一功能（§13）。

### 行為回歸測試（`evva eval`）

`go test` 能抓到壞掉的程式碼，但抓不到「系統提示詞的一次修改，悄悄讓代理不再於宣告完成前跑測試」這種問題——那不是編譯錯誤，你會在正式環境才發現。`evva eval` 就是這道缺席的閘門。

把你已經跑過的工作階段錄下來，再對修改後的設定重播，看看代理的**決策**是否改變：

```bash
evva eval list -sessions                                   # 有哪些可以錄製
evva eval capture <session-id> -name read-first \
    -desc "代理在編輯檔案前應先讀取它"
evva eval run                                              # 全部重播並評分
```

`evva eval run` 只要出現任何偏離就以非零狀態退出，因此可直接當成 CI 步驟或發版前的預檢。

**它比對什麼。** 工具呼叫的序列——用了哪些工具、順序為何、作用在哪些檔案或指令上。**不比對文字**。LLM 的輸出本來就不具決定性，比對散文只會得到一道隨機失敗的閘門；比對決策才會在行為真正改變時失敗。順序本身也是決策的一部分：把測試跑在編輯**之前**而不是之後，即使呼叫集合相同，也是真正的行為改變。

參數會被正規化以確保 fixture 可攜——路徑縮成檔名、指令縮成開頭的程式名——而基準線中未曾記錄的參數會被忽略，因此某個工具新增了選填參數並不會造成誤報。

**兩個層級。** 結構化比對是預設值，也是硬性閘門。`-judge` 為帶有散文 `expected_outcome` 的 fixture 增加一個建議性層級：額外一次模型呼叫，評估該次執行是否仍滿足該期望。這適用於沒有固定形狀的行為——拒絕、解釋——並且刻意**永不影響退出碼**。把機率性的評分器直接接進發版閘門只會產生不穩定的失敗，而不穩定的閘門終將被繞過。

**當某個 fixture 失敗時**，代表行為改變了——而那可能正是你的本意。重設基準線：

```bash
evva eval capture --update read-first
```

Fixture 位於 `testdata/evalfixtures/`，與程式碼一起版控。請保持精簡：每次重播都是真實、計費的模型流量，再乘上 fixture 數量。格式與種子集合詳見 `testdata/evalfixtures/README.md`。

`evva eval run` 是否成為發版前的必要步驟由你決定——evva 提供工具，不強加流程。

---

## 12. 日誌

每個代理的純文字日誌預設存放於 `$EVVA_HOME/logs/<agent-id>/<agent-id>.log`——`make install` 之後不需要額外設定即可找到。若要改寫到其他目錄，在 `.env` 中設定 `LOG_DIR=/your/path`。若要回到舊的 stdout-only 開發模式(日誌打到終端而非寫檔)，將 `LOG_DIR=` 明確設為空字串。`LOG_LEVEL=debug` 會揭露每次迭代的 `turn.start` / `llm.call` / `tool.dispatch` / `tool.result` 行——在除錯代理卡住或無限迴圈時非常實用。

---

## 13. LSP — 語言伺服器協定支援

evva 整合了語言伺服器（Language Server），讓終端機裡的程式碼代理能夠直接查詢語意資訊。

### 支援的操作

`lsp_request` 工具可讓代理查詢語言伺服器：

| 操作 | 說明 |
|---|---|
| `go_to_definition` | 跳至符號的定義位置 |
| `find_references` | 找出所有引用該符號的位置 |
| `hover` | 取得該位置的型別資訊與文件 |
| `document_symbols` | 列出檔案中的所有符號 |
| `workspace_symbol` | 以名稱搜尋整個工作區的符號 |
| `go_to_implementation` | 找出介面或型別的實作 |
| `call_hierarchy` | 追蹤呼叫圖（傳入／傳出呼叫） |

此外，LSP 伺服器會**自動推送診斷訊息**（錯誤、警告）——它們會以系統提醒的形式出現在對話中，代理無需主動請求。

---

### 逐步設定（以 Go 為例）

此範例使用 Go 與 gopls。同樣的模式適用於 TypeScript、Rust 或任何有 LSP 伺服器的語言。

#### 1. 安裝 LSP 伺服器

```bash
go install golang.org/x/tools/gopls@latest
```

確認已安裝在 PATH 上：

```bash
which gopls
# /Users/you/go/bin/gopls

gopls version
# golang.org/x/tools/gopls v0.21.1
```

#### 2. 在專案中啟動 evva

進入任何 Go 專案目錄（含有 `go.mod` 的目錄）並啟動 evva：

```bash
cd /path/to/your-go-project
evva
```

evva 會自動偵測 `go.mod` 與 PATH 上的 `gopls` — 無需撰寫設定檔。

若自動偵測未生效（少見情況），可建立最小設定檔：

```yaml
# .evva/lsp_servers.yml
servers:
  gopls:
    command: gopls
    extensions:
      ".go": "go"
    startupTimeout: "120s"
    maxRestarts: 3
```

#### 3. 驗證 LSP 是否正常運作

在 evva 對話中，請代理使用 LSP：

```
找出 server.go 中 Server 型別的定義
```

代理會以 `operation: "go_to_definition"` 呼叫 `lsp_request`。第一次請求會啟動 gopls（初始索引可能需要 30–60 秒）。後續請求即時回應。

手動檢查 LSP 伺服器狀態：

```
daemon_list
```

應該會看到 LSP 守護程序條目：

```
daemon l1 [lsp/running] server=gopls state=running restarts=0/3
```

#### 4. 測試常用操作

在 evva 中嘗試以下提示來測試不同的 LSP 功能：

- **定義：**「找出 `tool.go` 第 22 行 `Manager` 的定義」
- **引用：**「找出專案中所有引用 `Daemon` 的地方」
- **懸停：**「`tool.go` 第 22 行的 `ctx` 是什麼型別？」
- **符號：**「列出 `agent.go` 中的所有符號」
- **工作區搜尋：**「在工作區中搜尋與 'Agent' 匹配的符號」
- **呼叫階層：**「顯示 `NewTool` 的呼叫階層」

---

### 其他語言設定

#### TypeScript / JavaScript

```bash
npm install -g typescript-language-server typescript
```

當 `package.json` 存在且有 `.ts`/`.tsx` 檔案時自動偵測。

#### Rust

```bash
rustup component add rust-analyzer
```

當 `Cargo.toml` 存在時自動偵測。

#### 其他語言

建立 `.evva/lsp_servers.yml` 並填入對應語言的伺服器。常見伺服器：

| 語言 | 伺服器 | 安裝指令 |
|---|---|---|
| Python | pyright | `pip install pyright` |
| Zig | zls | [zigtools.org/zls](https://zigtools.org/zls/) |
| C/C++ | clangd | `apt install clangd` / `brew install llvm` |

Python 設定範例：

```yaml
servers:
  pyright:
    command: pyright-langserver
    args: ["--stdio"]
    extensions:
      ".py": "python"
    startupTimeout: "60s"
```

---

### 手動設定參考

在專案根目錄建立 `.evva/lsp_servers.yml`（專案層級），或放在 `~/.evva/lsp_servers.yml`（使用者層級，套用至所有專案）。相同伺服器名稱下，專案層級設定會覆蓋使用者層級。

完整設定格式：

```yaml
servers:
  gopls:
    command: gopls                    # 必要：二進位檔名稱或路徑
    args: []                          # 選用：CLI 參數
    extensions:                       # 必要：副檔名 → 語言 ID
      ".go": "go"
    env:                              # 選用：環境變數
      GOPATH: "${HOME}/go"
    startupTimeout: "120s"            # 選用：等待初始化的最長時間（預設 30s）
    maxRestarts: 3                    # 選用：崩潰復原上限（預設 3）
```

`command`、`args` 與 `env` 的值支援環境變數展開（`${VAR}`、`${HOME}`）。

---

### 使用方式

`lsp_request` 工具為**延遲載入** — 代理在需要 LSP 功能時會透過 `tool_search` 發現它。你可以直接向代理提出類似以下的問題：

- 「`UserService` 定義在哪裡？」
- 「找出所有引用 `authenticate` 的地方」
- 「這個變數是什麼型別？」
- 「列出 `handler.go` 中的所有符號」
- 「誰呼叫了 `processRequest`？」

代理會在適當時機自動使用 `lsp_request`。

---

### 疑難排解

**「gopls not found in PATH」**
安裝缺少的伺服器（見上方安裝指令），重新啟動 evva 後再試。

**「No LSP server configured for extension .py」**
在 `.evva/lsp_servers.yml` 中新增該語言的伺服器設定。錯誤訊息中的提示會建議應安裝的伺服器名稱。

**伺服器已啟動但請求無回應**
gopls 首次啟動時需要時間為專案建立索引。大型專案可能需要 60–120 秒。請在設定中提高 `startupTimeout`，並在第一次 `lsp_request` 後等待 — 後續請求就會很快。

**沒有出現診斷訊息**
診斷訊息會在 `lsp_request` 開啟檔案後出現。如果你用 `write`/`edit`/`bash` 編輯了檔案，請對該檔案呼叫 `lsp_request` 以重新整理診斷訊息。

**殘留的 gopls 程序**
執行 `pkill gopls` 清理。evva 在關閉時會終止伺服器，但若 evva 崩潰，伺服器程序可能殘留。

---

## 14. 以 evva 開發 — SDK（開發者指南）

前面把 evva 當作應用程式介紹；evva 同時也是可嵌入的 Go SDK：另一支程式可以 `import "github.com/johnny1110/evva/pkg/agent"`，組出自己的 ReAct agent — 自訂 LLM 提供者、自訂工具、自己的人格、權限政策與 UI — 不需 fork，也不必碰 agent 迴圈。

整個公開介面都在 `pkg/*` 底下。Go 的 `internal/` 規則在編譯期強制這條界線：下游模組若不小心引用 `evva/internal/...` 會無法編譯。自 `v1.0.0` 起，旗艦的 `cmd/evva` 本身也完全建構在 `pkg/*` 之上，所以內建應用程式做得到的事，你的程式也做得到。

```bash
go get github.com/johnny1110/evva@v1.0.0
```

### 快速開始 — 約 40 行的完整宿主程式

一個宣告式的 `agent.Config` 搭配幾個選項，就能得到完整體驗 — 內建終端機 UI、人格 `/profile` 切換、權限提示、`/resume` 與 `/compact`。`agent.New` 會吸收整個啟動流程：解析人格（找不到時退回 `evva`）、自動載入 `EVVA.md` 與 `~/.evva/memory/` 目錄及技能目錄、載入權限規則庫，並安裝核准／提問 broker。

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/johnny1110/evva/pkg/agent"
    "github.com/johnny1110/evva/pkg/config"
    _ "github.com/johnny1110/evva/pkg/llm/builtins" // 註冊 anthropic/deepseek/openai/ollama
    "github.com/johnny1110/evva/pkg/ui/bubbletea"
)

func main() {
    cfg := config.Get() // 或 config.Load(config.LoadOptions{AppName: "myapp", AppHome: ...})

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    tui := bubbletea.New(cfg.AppHome) // 內建參考 TUI（實作 ui.UI）

    ag, err := agent.New(agent.Config{AppConfig: cfg},
        agent.WithSink(tui),        // agent 將事件送進 UI
        agent.WithRootContext(ctx), // Ctrl-C 會關閉所有背景工作
    )
    if err != nil {
        panic(err)
    }
    defer ag.Shutdown()

    tui.Attach(ag.Controller()) // 把 agent 的 controller 檢視交給 UI
    _ = tui.Run(ctx)
}
```

不需要 TUI？拿掉它即可：用 `agent.New(agent.Config{AppConfig: cfg, PermissionMode: "bypass"})` 建構，再呼叫 `ag.Run(ctx, "你的提示")`。沒有 sink 時，agent 會自動拒絕核准請求（避免卡住）；`"bypass"` 則在可信任／CI 環境中全部自動放行。

需要拿到資料而不是散文？給執行掛上一個 JSON schema——`Run` 就會回傳可直接
unmarshal 的合規 JSON：

```go
schema := json.RawMessage(`{"type":"object","required":["bugs"],
    "properties":{"bugs":{"type":"array","items":{"type":"string"}}}}`)

ag, _ := agent.New(agent.Config{AppConfig: cfg, PermissionMode: "bypass"},
    agent.WithStructuredOutput(schema))

out, err := ag.Run(ctx, "find bugs in ./pkg/foo")
if errors.Is(err, agent.ErrNoStructuredOutput) {
    // 模型以散文作答；out 是散文——可重試或改走 fallback
}
var v struct{ Bugs []string `json:"bugs"` }
_ = json.Unmarshal([]byte(out), &v)
```

模型會看到一個一次性的 `structured_output` 工具，其輸入 schema 就是你給的
schema（支援 schema 約束的 provider 會在伺服器端強制形狀），模型第一次呼叫它
執行就終止。僅限 headless：不要與互動式 UI 併用。

### 擴充點一覽

每個部分都能透過 `pkg/*` 的接縫替換：

| 想要… | 使用 |
| --- | --- |
| 新增 LLM 提供者 | 在 `llm.DefaultRegistry()` 註冊工廠函式；你的 `llm.Client` 需實作 `Name` / `Model` / `SupportsDeferLoading` / `Complete` / `Stream` / `Apply`。 |
| 新增工具 | 實作 `tools.Tool`；以 `agent.WithCustomTool(name, factory)` 傳入，或註冊到 `toolset.DefaultRegistry()`。 |
| 最終答案輸出為 JSON | `agent.WithStructuredOutput(schema)`（僅限 headless）；`Run` 回傳 payload，`errors.Is(err, agent.ErrNoStructuredOutput)` 偵測模型未履約。 |
| 新增人格 | `agent.BuildAgentRegistry` + `reg.Register(agent.AgentDefinition{...})`（或在 `<AppHome>/agents/<name>/` 放置檔案）；以 `Config.Personas` + `Config.Persona` 傳入。會驅動 `/profile` 與子代理。 |
| 控制核准 | `Config.PermissionMode`、`Config.PermissionStore`，或自訂 `agent.WithPermissionBroker`（以 `permission.NewBroker` + `SetOnRequest` 建立）。 |
| 自製 UI | 實作 `ui.UI`；透過完全公開的 `ui.Controller` 驅動 agent。或直接嵌入 `pkg/ui/bubbletea`。 |
| 提供技能（skill） | `skill.NewRegistry()` + `Add(...)`（程式碼）或放置 `SKILL.md` 檔案；以 `agent.WithSkillRegistry` 傳入。 |
| 加入生命週期掛鉤 | 在 `.evva/settings.json` 加入 `hooks` 區塊；掛鉤會在 SessionStart、UserPromptSubmit、PreToolUse、PostToolUse、Stop、Notification 事件觸發。詳見[生命週期掛鉤](#生命週期掛鉤)。 |
| 使用自訂家目錄 | `config.Load(config.LoadOptions{AppName, AppHome, ...})` → `Config.AppConfig`。 |

### 穩定性與延伸閱讀

`v1.0.0` 讓 **Stable** 等級套件受主版號承諾保護：`pkg/agent`、`pkg/config`、`pkg/event`、`pkg/llm`、`pkg/tools`、`pkg/toolset`、`pkg/permission`、`pkg/ui`、`pkg/skill`、`pkg/constant`。Experimental 等級套件（`pkg/ui/bubbletea`、`pkg/tools/lsp`、`pkg/observable`、`pkg/tools/kits`）在次版號仍可能變動。

- [`integration.md`](../en/integration.md)（英文）— 逐步整合教學。
- [`docs/contributing/extending.md`](../../contributing/extending.md)（英文）— 完整參考：每個公開套件、每個擴充點，以及無法覆寫的部分。
- [`docs/contributing/sdk-stability.md`](../../contributing/sdk-stability.md)（英文）— 各套件穩定性等級與如何在 `go.mod` 釘選 evva。
- [`examples/full-host/`](../../../examples/full-host/main.go) — 可執行的完整宿主（TUI + 人格 + 權限），獨立模組。

### 生命週期掛鉤

掛鉤（hook）是使用者編寫的 shell 指令或 HTTP webhook，會在 agent 迴圈的六個時機點觸發。在 `.evva/settings.json` 中設定：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          {
            "type": "command",
            "command": "jq '.tool_input' | grep -q dangerous && exit 2 || exit 0",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

**事件：**
- `SessionStart` — agent 首次執行時觸發一次
- `UserPromptSubmit` — 每次使用者輸入送出前觸發
- `PreToolUse` — 每次工具執行前觸發；可阻擋、變更輸入或覆寫權限
- `PostToolUse` — 工具執行後觸發；可將額外內容附加到結果中
- `Stop` — agent 到達終止回合時觸發；可重新進入迴圈一次
- `Notification` — 頻外事件觸發（如達到迭代上限）

**掛鉤類型：**
- `type: "command"` — shell 指令，stdin 接收 JSON 承載。exit 0 → 解析 stdout 為決策；exit 1 → 非阻塞錯誤（記錄日誌）；exit 2 → 阻擋。
- `type: "http"` — HTTP POST，預設為非同步。

專案掛鉤（`.evva/settings.json`）比使用者掛鉤（`<APP_HOME>/settings.json`）先觸發。格式錯誤的設定檔會產生啟動警告，agent 仍會正常啟動。
- [`examples/minimal-host/`](../../../examples/minimal-host/main.go) — 可執行的精簡宿主（自訂提供者 + 工具 + 技能）。
