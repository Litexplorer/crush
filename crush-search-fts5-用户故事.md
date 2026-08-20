# 消息搜索优化：FTS5 全文搜索 + 跳转定位 —— 用户故事拆解

> 骨架优先：每阶段 = 契约 → 跑通骨架 → 核心主干 → 按需补充 → 延迟抽象 → 回归验证，每门验证通过后提交。

## 背景

消息搜索（Ctrl+K）已上线，但存在两个已知缺口：

1. **性能**：`SearchMessages` 使用 `LIKE '%q%'` 全表扫描（`internal/db/sql/messages.sql:61`），无法利用 `idx_messages_created_at` 索引，消息量大时变慢。升级为 SQLite FTS5 全文索引（已验证 modernc.org/sqlite 3.53.3 内置 FTS5）。
2. **体验**：搜索结果回车跳转到命中会话后，视口停在会话底部，用户看不到命中消息。`ActionSelectSearchResult`（`internal/ui/dialog/actions.go:38`）已携带 `MessageID` 但未消费。

## 技术事实（已勘察确认）

- FTS5 可用：`CREATE VIRTUAL TABLE ... USING fts5` + `MATCH` 查询在 modernc sqlite 3.53.3 下工作正常。
- `messages.parts` 是 JSON 文本（`[{"text":"...","type":"text"}]`），LIKE 与 FTS5 unicode61 tokenizer 都能匹配其中的词。
- 迁移机制：goose（`internal/db/connect.go:163`），`//go:embed migrations/*.sql`，新文件自动执行；格式 `-- +goose Up/Down` + `StatementBegin/End`。
- `Chat` 已有 `idInxMap map[string]int`（`internal/ui/model/chat.go:92`，MessageID → list index），`list.SetSelected`（`internal/ui/list/list.go:767`）+ `list.ScrollToIndex`（:439）可直接定位。
- `setSessionMessages`（`internal/ui/model/ui.go:1421`）末尾调用 `m.chat.SelectLast()`，因此跳转滚动必须在 `setSessionMessages` 之后执行（loadSessionMsg handler 内追加 cmd）。
- `loadSessionMsg` handler 在 `internal/ui/model/ui.go:761`；`ActionSelectSearchResult` 在 `:1895`。

## 用户故事

### P0 数据层

#### US-F1: FTS5 迁移（虚拟表 + 触发器 + 回填）

- 契约：新迁移 `internal/db/migrations/<ts>_add_messages_fts.sql`
  - `CREATE VIRTUAL TABLE messages_fts USING fts5(parts, content='messages', content_rowid='id')`（外部内容表，避免双份存储）
  - 3 个同步触发器：AFTER INSERT / AFTER DELETE / AFTER UPDATE OF parts
  - `INSERT INTO messages_fts(messages_fts) VALUES('rebuild')` 回填存量数据
  - Down：DROP TRIGGER × 3 + DROP TABLE
- 验证门 1：`go run` 或测试库跑迁移成功；插入/更新/删除一条消息后，`SELECT count(*) FROM messages_fts` 与 `messages` 行数一致。
- 提交 1。

#### US-F2: SearchMessages 改用 FTS5 MATCH + 转义

- 契约：
  - `internal/db/sql/messages.sql` 改写为 `JOIN messages_fts fts ON fts.rowid = m.id WHERE messages_fts MATCH ?`（排序/分组不变）
  - 跑 `/tmp/sqlc-bin/sqlc generate` 重新生成；适配参数类型（MATCH 参数不再是 `sql.NullString` 的来源，需看生成结果）
  - service 层（`internal/message/message.go:511`）做 FTS5 短语转义：`"` → `""`，整体包裹为 `"query"` 短语；空词短路保留
- 验证门 2：`go test ./internal/message/... ./internal/backend/...` 全绿（现有种子测试断言大小写不敏感、排序、LIMIT 不变）；新增测试：含引号/特殊字符的查询不报错、多词短语匹配。
- 提交 2。

### P1 UI

#### US-G1: 跳转后滚动定位到命中消息

- 契约：
  - `Chat.ScrollToMessage(id string) tea.Cmd`（`internal/ui/model/chat.go`）：查 `idInxMap` → `SetSelected(idx)`（不可选 item 跳过选中）→ `ScrollToIndex(idx)` → `showScrollbar()`
  - `UI.pendingScrollToMessageID string` 字段：`ActionSelectSearchResult` 处理时写入 `msg.MessageID`；`loadSessionMsg` handler 在 `setSessionMessages` 后消费并清空
- 验证门 3：编译通过；model 级测试（若有可行途径）或手工 E2E。
- 提交 3。

#### US-G2: 回归验证 + E2E 人工校验清单

- 回归：`go build ./...` + 相关包测试全绿（message/backend/ui/model/dialog）
- E2E 人工清单（用户执行）：
  1. 启动编译出的 crush，打开会话 A 发消息含关键词 `quick`
  2. Ctrl+K 搜索 `quick`，回车跳转 → **预期：视口滚动到命中消息处，而非停在底部**
  3. 搜索含引号内容（如 `say "hi"`）→ **预期：不报错，能返回结果**
  4. 空搜索框回车 → 无结果占位，不崩溃
- 提交 4（如无改动则不提交）。

## 行为差异记录（FTS5 vs LIKE，可接受）

| 场景 | LIKE 行为 | FTS5 行为 |
|------|-----------|-----------|
| 大小写 | 不敏感（ASCII） | 不敏感（unicode61 默认） |
| 部分词 `quick br` | 子串匹配命中 | 短语 token 匹配，`br` 非完整 token 不命中 |
| 标点 `foo.bar` | 子串命中 | 拆成 `foo`/`bar` 两 token |
| 引号 `say "hi"` | 命中 | 转义为短语后命中 |

## 非目标（YAGNI）

- 排序改为 BM25 rank（保持 created_at DESC）
- 高亮命中词/预览片段改造
- 正则搜索、前缀通配搜索
- FTS 索引列扩展到 role/session 标题

## 开放问题

- FTS5 对 CJK（中文）分词：unicode61 按字切分（不按词），中文搜索命中粒度可能变粗——由真实需求触发再评估。
