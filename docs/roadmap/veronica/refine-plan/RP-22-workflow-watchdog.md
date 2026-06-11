# RP-22 — Workflow 級看門狗（task 卡齡 / 信箱積壓的機械偵測）

> 狀態：**✅ 已完成（2026-06-11，feature/RP-22-workflow-watchdog）** ｜ 階段：**第五波** ｜ 優先：**P1** ｜ 日期：2026-06-11
> 落地註記：掃描器掛在 supervisor timerTick（與 budget/stall/retention sweep 同點），以
> `workflowSweepInterval`（10 分鐘，測試可縮）節流——非 service 級巡檢，因為通知走 `notifyOps`（durable
> bus mail，leader + operator 同 RP-13/14 通道）而 bus 在 swarm 層。防刷屏標記全在記憶體、tick goroutine
> 獨佔（vacuumDay 模式，無鎖）：task 以 (id, status, updated_at) 為 stay key——`updated_at` 只在狀態跃迁時
> 變動，所以即是「進入現狀態的時刻」；mailbox 以 episode 為界（積壓清空即重置）。重啟後標記歸零 → 仍卡
> 的 task 會再提醒一次（與 RP-14 同哲學，視為 feature）。對 ticket §2.3 的兩個微調：①「member 的積壓告
> 警」不會寄回該 member 自己的信箱（leader 積壓 → 只給 operator），否則告警餵養積壓自身；②event log 不
> 另造合成行——notifyOps 郵件本身 durable 可查、`/metrics` 有計數，與 RP-14 同口徑。`store.OldestUnread`
> 排除 claimed（被折進 in-flight run 的信屬 RP-14 管轄）。
> 觸發：Sunday swarm 重整。觀察到的模式：leader「不主動協調」時，task 卡在 `running`/`verifying` 沒人催、隊友回報石沉大海——**run 沒卡（RP-14 不會叫）、協議有教（RP-12 prompt 在）、但工作流死了，系統毫無感知**。
> 關聯：[RP-14](RP-14-stuck-run-watchdog.md)（run 級 stall——本文是它的 ledger 級姊妹）、[RP-12](RP-12-advice-loop-closure.md)（協議級閉環——本文給它機械後盾）、[RP-1](RP-1-messaging-reliability.md)（unread 積壓偵測順手覆蓋其回歸）、[RP-17](RP-17-durable-event-log.md)（通知入 event log）
> 請求者：Sunday。**無 Sunday-specific code。**

---

## 1. Problem（observed）

evva 現有三層守護各管一段，中間有條縫：

| 層 | 守護 | 管什麼 | 不管什麼 |
| --- | --- | --- | --- |
| run | RP-14 stall watchdog | 一次 run busy 太久 | run 之間的事 |
| 協議 | RP-12 closure prompt | leader「應該」閉環 | leader 沒做時無人知曉 |
| **ledger** | **（無）** | — | **task 卡齡、信箱積壓** |

具體縫隙：

1. **task 卡齡**：task 進 `running` 後 assignee 掛了/忘了，或交付進 `verifying` 後 leader 一直不驗收——5 狀態機只保證**合法跃迁**，不保證**有人推進**。看板上一張卡躺三天，唯一發現方式是 operator 自己盯 Web。
2. **信箱積壓**：成員 mailbox 有 unread 但遲遲沒被 drain——正常喚醒鏈下不該發生（RP-1 的 level-triggered drain + rescanTick），所以**一旦發生就是 RP-1 類回歸或成員被凍結/暫停被遺忘**，值得一個獨立 tripwire。

協調品質目前完全靠 leader 自覺；框架應該把「工作流卡住」變成跟「run 卡住」一樣的一等訊號。

## 2. Proposal

`settings` 加兩個保險絲（語義對齊 RP-14：省略 = 合理預設、`"0"` = 關閉）：

```yaml
settings:
  task_stale_threshold: "24h"    # task 停留在 running/verifying 超過即提醒；"0" 關閉
  mailbox_stale_threshold: "30m" # unread 信齡超過即告警（bus 健康 tripwire）；"0" 關閉
```

1. **掃描器**：掛在既有的每日/定期 maintenance 節奏（與 RP-16 vacuum 同類的 service 級巡檢，但頻率較高，如每 10 分鐘一次輕量 SQL）。
2. **task 卡齡**：`running`/`verifying` 停留超閾 → 通知 **leader**（bus 訊息，附 task id/title/assignee/卡齡/現狀態）＋ operator（Web timeline）。**每 task 每次進入該狀態至多一封**（比照 RP-14 的「每 run 一次」防刷屏；退回 running 重新計時）。`suspended` 豁免——那是刻意停放。
3. **信箱積壓**：unread 最老一封超閾 → 通知 operator（Web）＋ event log；若積壓者非 leader 也抄送 leader。凍結（frozen）成員豁免 mailbox 告警？**不豁免**——「他被凍結了但信還在堆」正是 operator 要知道的事，訊息註明凍結狀態即可。
4. **可觀測**：`/metrics` 加 `tasksStale` / `mailboxStale` 計數；`task_list` 對超閾 task 標 `⏳ stale 26h`。

## 3. Why evva（not Sunday）

卡齡的事實全在 `.vero/vero.db`（tasks 狀態時間戳、messages claim 狀態）——只有 swarm runtime 讀得到、也只有它能在正確時點戳 leader。Sunday 能做的只有在 persona 裡寫「記得催」——這正是現在失效的東西。

## 4. Acceptance

- task 在 `running` 超閾 → leader 收到恰好一封提醒（含 task 細節）；task 被推進後再卡 → 重新計一封。
- `verifying` 超閾同理；`suspended` 不告警；`"0"` 全關時零行為差異。
- 製造一封 unread 老信（凍結成員）→ operator 在 Web/event log 看到 mailbox 告警。
- 通知本身走既有 bus/timeline 機制，無新傳輸面；`-race` 綠。
- `/metrics` 計數隨告警遞增；`task_list` 顯示 stale 標記。

## 5. Notes

- 刻意**不做**「訊息已讀未回」偵測——「回了沒」需要 reply 語義連結，機械判定誤報率高；那一層留給 RP-12 的協議 + reviewer 類角色的人工復盤。本 RP 只抓**客觀**可判的卡齡。
- 與 RP-14 的分工一句話：RP-14 管「正在跑的卡住」，本 RP 管「沒人跑的卡住」。
- 通知文案附 suggested action（催 assignee / task_verify / 解凍成員），讓 leader 醒來第一回合能動——比照 Sunday webhook 的 `suggested_action` 哲學。
