# RP-23 — Worker 任務提案（bottom-up 工作入口，不破壞 leader 單一寫者）

> 狀態：**✅ 已完成（2026-06-11，feature/RP-23-worker-task-proposals）** ｜ 階段：**第五波** ｜ 優先：**P2** ｜ 日期：2026-06-11
> 落地註記：照 Option A（`0005_proposals.sql` + `store/proposals.go`）。比提案多三個決定：①`ref_task`
> 刻意**不是** FOREIGN KEY——真 FK 會把 proposals 捲進 RP-16 vacuum 的 transitive pinning fixpoint（提案
> 釘住已完成 task 或反之）；它是 audit pointer，兩邊可獨立歸檔。②`proposal_accept` 的「原子」做成**單一
> store 交易**（`AcceptProposal`：claim open→accepted ＋ 直接以 `running` INSERT task ＋ 回填 ref_task）——
> 比工具層串 task_create+task_assign 更強：併發裁決恰一勝者、永不產生孤兒 task；DAO 並做 leader-only
> Actor 檢查（與 TransitionTask 同口徑，縱深防禦）。③多給 leader 一個 `proposal_list`——accept 需要
> proposal_id，context 壓縮後 leader 必須能重查（與 task_list 存在的理由相同）。decided 提案進 RP-16
> 歸檔（archive kind "proposal"）；`GET /api/swarm/{id}/proposals` 已就緒，FE lane 留給 FE 波（ticket
> 原文也標 FE-5/FE-7 範疇）。Web 可見性現狀：API + leader 通知信（Messages/Timeline 可見）。
> 觸發：Sunday swarm 重整。實例：risk-monitor 巡檢發現裸倉，要 trader 補停損——這在精神上是一張 task（要追蹤、要驗收、要留痕），但 worker 開不了票，只能走 `send_message`：**看板無痕、無人驗收、reviewer 復盤找不到**。
> 關聯：[RP-6](RP-6-completed-task-scaling.md)（task 規模化——提案被接受後進同一帳本）、[RP-12](RP-12-advice-loop-closure.md)（提案被打回時的閉環紀律同樣適用）、`internal/swarm/tools/set.go:75-85`（現行 role→tool 邊界）、design v1 的 task-ledger 單一寫者不變量
> 請求者：Sunday。**無 Sunday-specific code。**

---

## 1. Problem（observed）

`toolNamesForRole`（`internal/swarm/tools/set.go:75-85`）給 worker 的任務面只有唯讀（`my_tasks`/`task_get`）。單一寫者保護的是**狀態機完整性**——這要守住；但它順帶把「**提出**工作」也擋掉了。後果：

1. worker 發現的工作（風控缺陷、回歸、值得深挖的線索）只能折進聊天流，leader 忙時就丟了；
2. 看板只反映 top-down 視角——團隊實際工作量的 bottom-up 半邊不可見；
3. 「誰提的、接了沒、為什麼不接」無痕，協作學習迴路（RP-12）少了一條邊。

## 2. Proposal（提案與帳本分離——Option A，推薦）

**新表 `proposals`**（與 tasks 分開，ledger 不變量零觸碰）：
`proposals(id, proposer, title, spec, suggested_assignee, status: open→accepted|declined, decided_by, decide_note, ref_task, created_at, decided_at)`。

新工具：

- **worker：`task_propose {title, spec, suggested_assignee?}`** → 建一筆 `open` 提案 + bus 通知 leader（附提案內容）。
- **leader：`proposal_accept {proposal_id, assignee?}`** → 原子地走既有 `task_create`+`task_assign`，回填 `ref_task`，通知 proposer。
- **leader：`proposal_decline {proposal_id, note}`** → `declined` + 通知 proposer（note 必填——RP-12 的閉環紀律在這裡是 schema 強制，不只是 prompt 美德）。

配套：

- `task_list`（leader）尾端附 `open proposals: N`；Web 看板加 proposals 收件匣（lane 或 badge，FE-5/FE-7 範疇）。
- 提案協議注入 worker protocol 一句（`teamprompt.go`）：「發現該追蹤的工作，用 `task_propose` 放上看板，別只埋在訊息裡。」
- retention：`declined` 與 `accepted` 的提案行進 RP-16 的歸檔窗口。

**Option B（備選，不推薦）**：直接讓 worker `task_create` 出 `pending` 未指派列＋ proposer 欄。優點是少一張表；缺點是打穿「只有 leader 寫 tasks」這條從 day-1 講到現在的不變量，且 pending 提案混進 pending 任務，leader 的 `task_list` 語義變糊。**狀態機單一寫者是 Veronica 的招牌保證，為省一張表打破它不值。**

## 3. Why evva（not Sunday）

「誰能把工作放上看板」是 swarm 協作拓樸，與 RP-11（誰能拉 lever）、RP-12（誰要閉環）同族。Sunday 端的 workaround（persona 教 worker「重要的事多發幾次訊息」）只會製造刷屏，不會製造可追蹤性。

## 4. Acceptance

- worker `task_propose` → leader 收到通知；`proposal_accept` → 帳本出現對應 task（正常 assign 流），proposer 收到「已接受 → task #N」；`proposal_decline` 無 note 被拒收。
- worker 對 tasks 表仍無任何寫路徑（單一寫者不變量回歸測試）。
- 提案全生命週期入 RP-17 event log；Web 可見 open proposals。
- 併發：兩個 worker 同時提案、leader 同時裁決，無 race（`-race` 綠）。

## 5. Notes

- 提案**不是**任務的影子狀態機——`open/accepted/declined` 三態終局，沒有 reopen；要重提就再開一筆（保留完整決策史）。
- leader 自己不需要 `task_propose`（他直接 `task_create`）；工具按角色注入，與現行對稱。
- Sunday 落地畫面：risk-monitor 巡出裸倉 → `task_propose{title:"補 ETH 停損", suggested_assignee:"trader"}` → friday 醒來 accept → trader 修復 → friday 驗收——整條鏈在看板上，reviewer 復盤直接引用。
