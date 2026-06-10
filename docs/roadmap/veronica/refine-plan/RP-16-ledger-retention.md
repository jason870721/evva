# RP-16 — Ledger retention（messages / tasks 的歸檔與瘦身）

> 狀態：**✅ 已完成（2026-06-10，feature/RP-16-ledger-retention）** ｜ 日期：2026-06-10 ｜ 波次：**第四波（運營硬化）** ｜ 優先：P1
> 觸發：2026-06-10 健康檢查——設計言明「v1 never deletes history」（`space.go` removeAgent
> 註解），messages/tasks 只增不刪。週級沒事；**月級的 24/7 集群**（Sunday：watchdog 一天
> 720 醒、每醒至少一輪 mail）會讓全量掃描的 API 與 web 載入逐漸變慢。
> 上層：[`../health-check-2026-06-10.md`](../health-check-2026-06-10.md) ｜ 前文：[RP-6](RP-6-completed-task-scaling.md)（已做 task 分頁＋active 預設，本 RP 是它的「物理刪除」續集）

---

## 1. 現況盤點（file:line 證據）

| # | 事實 | 位置 | 意義 |
| --- | --- | --- | --- |
| S1 | messages 表只增（read_at/claimed_at 標記、無刪除原語） | `store/messages.go` | 增長無界 |
| S2 | tasks 同（RP-6 加了分頁/計數，未刪） | `store/tasks.go`、`service.go:880` | 同上 |
| S3 | `Messages` API 仍是近況全掃 | `service/service.go:950` | 變慢的第一現場 |
| S4 | Close 時 WAL TRUNCATE checkpoint 已做 | `store/store.go:85-95` | ✅ 檔案層乾淨 |
| S5 | sqlite 本身百萬行無壓力 | — | 痛點在 API/UI 層，不是 DB 引擎 |

## 2. 設計方向

1. **Retention 規則（保守預設）**：
   - messages：`read_at` 非空 **且** 超過 `retention_days`（預設 30）→ 可清。
   - tasks：`completed` 且超期 → 可清（連同其 verify_note）。
   - **絕不動**：unread、claimed、`pending/running/suspended/verifying` 的任務。
2. **歸檔而非蒸發**：清理前先 dump 成 `<workdir>/.vero/archive/YYYY-MM.jsonl.gz`
   （append、可重讀），然後 DELETE ＋ 週期 `VACUUM`/checkpoint。要查古早史就翻歸檔檔。
3. **入口**：
   - 手動：`evva swarm vacuum <ref> [--days N] [--dry-run]`（dry-run 印將清理的行數）。
   - 自動：service 每日 local 凌晨跑一次（時區語意沿 `pkg/common` 約定）；
     `settings.retention_days: 0` = 完全關閉（保留今天的「never deletes」行為）。
4. webapi 加 `POST /api/swarm/{id}/vacuum`（guard 後）供 FE 一鍵。

## 3. 驗收（DoD）

1. `--dry-run` 數字與實清一致；活資料（unread/claimed/active task）清理前後逐 byte 不變。
2. 歸檔檔可重讀（一個小 reader 測試）；清理後 unread 收發、claim/settle、task 狀態機
   全部既有測試綠燈。
3. `retention_days: 0` 時零行為變化。
4. 月級資料量模擬（10 萬 messages）下 `Messages` API 延遲回到常數級。

## 4. 非目標

- 跨 space 聚合歸檔、壓縮格式可調、線上重建索引——都不做，保持一個檔案一個月的樸素形態。

## 5. 實作記錄與偏離（2026-06-10）

1. **核心在 store 層**：`store.Vacuum(cutoff, dryRun)`（`internal/swarm/store/retention.go`）
   ——整趟持有寫鎖：選行 → 寫歸檔 → 同交易 DELETE → checkpoint+`VACUUM`。歸檔寫失敗
   即中止、零刪除；刪除失敗後重跑可能在歸檔重複 append（append-only log 語意，無害）。
2. **比 PRD 多的一條規則——引用釘住（pin）**：schema 有兩條外鍵
   （`messages.ref_task`、`tasks.parent_id`，且 DSN 開了 `foreign_keys=1`），所以
   「completed 且超期」不夠：被**存活行**引用的任務必須留下，且 pin 是傳遞的
   （存活子任務 → 釘住 completed 父任務 → 再釘住祖父），用固定點迴圈算；同批內
   父子一起刪則靠 `PRAGMA defer_foreign_keys=ON` 免排序。
3. **時鐘語意**：messages 用 `read_at`（消費時間）當退場時鐘——東西在被讀完
   `retention_days` 天後才會消失，比用 created_at 更保守；tasks 用 `updated_at`
   （完成戳）。歸檔分桶用行自己的 `created_at` 本地月份（`pkg/common` 時區紀律）。
4. **自動入口**：掛在 supervisor 既有 timerTick（`sweepRetention`），每本地日一次
   ＋ **service 啟動後補跑一次**（睡過午夜的機器醒來先補課）；實際清理跑在獨立
   goroutine（wg 追蹤，teardown 先排乾再關 DB），`vacuumBusy` 防疊跑。
   `retention_days`：省略 = 預設 **30**、`"0"` = 永不刪（沿 stall 旋鈕的字串慣例，
   避免 yaml int 0 與省略不可分）。Go 直建的 Settings 零值 = 關——單元測試空間
   永遠不會被突襲清理。
5. **手動入口**：`evva swarm vacuum <ref> [--days N] [--dry-run]` → 新 guarded
   `POST /api/swarm/{id}/vacuum`（空 body = 用配置窗口實跑）。days<=0 解析順序：
   space 配置 → 預設 30（operator 顯式要求可越過「off」）。**FE 一鍵按鈕沒做**——
   端點已就緒，但沒有 dry-run 預覽的一鍵刪除是個 footgun，留給之後的 FE 迭代。
6. **DoD 驗證**：#1 dry-run 與實跑數字一致＋倖存行 byte-identical（DeepEqual）＋
   幂等（第二趟 0/0）；#2 `store.ReadArchive` 重讀多 gzip member 歸檔（reader 測試）；
   #3 `"0"` 行為零變化（supervisor 測試）；#4 實測 10 萬 messages：清理前
   `ListMessages` ~302ms → 清理後 ~0.3ms，清理本身 ~1.24s（一次性量測，未入 repo）。
   全套 `go test ./...` + `-race` 綠。
