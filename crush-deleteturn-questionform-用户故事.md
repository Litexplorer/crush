# 删除 Turn 功能重做（方案 A：QuestionForm 确认对话框）用户故事拆解

## 背景

删除 turn 功能（`6f1bb124`）因光标/editor 错位已回退（`190e8626`）。根因：`renderEditorView` 插入 `confirmView` 一行，但 editor 布局高度固定为 `textarea.Height() + editorHeightMargin`（margin=2）未同步，内容超高导致 textarea 及光标被下推/裁剪。

**方案 A**：确认提示改用现有 `dialog.QuestionForm`（`activeInline` 内联编辑器机制），该机制布局高度计算已内置支持（`generateLayout` 中 `editorHeight = m.activeInline.Height(...)`），不会破坏 editor 区域。

## 技术事实（已核实）

- `activeInline` 机制：UI 字段（`ui.go:263`），非 nil 时在 editor 区域渲染内联编辑器，布局高度走 `m.activeInline.Height(editorWidth)`（`ui.go:3540-3548`）
- 按键路由已内置：`ui.go:2547-2558`，`m.activeInline.HandleKey(msg)` 返回 `done` 时自动 `m.activeInline = nil` + `m.textarea.Focus()` + `updateLayoutAndSize()` —— **关闭与焦点归还零成本**
- QuestionForm：`dialog.NewQuestionForm(sty, batch)`，`OnAnswer func([]question.Answer)` / `OnCancel func()`
  - 单问题 batch（len==1）不显示 tabs/confirm 页，直接渲染 YesNo（`question_form.go:91-96`）
  - YesNo 默认选中 No（`question_yesno.go:52`），`Response()` 返回 `question.Answer{Yes: *bool}`（`question_yesno.go:101`）
- 现有打开入口：`openBatchFormDialog`（`ui.go:4603-4621`）——但它的 OnAnswer 绑死 workspace QuestionAnswer，**删除确认需要本地回调**，需新增类似函数或参数化
- QuestionForm 关闭路径：QuestionForm 的 confirm/close 会调用 `OnAnswer`/`OnCancel`；`handleQuestionNotification`（`ui.go:4626`）在 question 被外部解决时关闭——删除确认**不应**走 workspace question 通道，需独立处理
- 删除数据链：`SelectedMessageID()`（被回退，需重加，`chat.go`）→ `Workspace.DeleteTurn(ctx, sessionID, anchorID)`（`workspace/app_workspace.go:108` + client_workspace.go:232，**仍在**）→ `messages.DeleteTurn`（message.go:233，**仍在**）→ pubsub DeletedEvent → `ui.go` DeletedEvent 分支 RemoveMessage（**分支已随回退移除，需重加**）
- 消息 → 多 item 渲染：一条消息 = 内容 item + 每个 tool call item + end-of-turn info footer（`chat.AssistantInfoID`）
- 键位：`km.Chat.DeleteTurn`（keys.go:267，被回退需重加），原绑定 ctrl+x
- 焦点语义：删除确认时 focus 应在 editor（QuestionForm 激活时 focus=uiFocusEditor，`openBatchFormDialog` 内处理）

## 方案对比（已决策）

| 方案 | 确认 UI | 布局影响 | 结论 |
|------|---------|---------|------|
| A：QuestionForm + activeInline | 内联 YesNo 表单 | 高度走 `activeInline.Height()`，布局同步 ✅ | **采用** |
| B：confirmView 插入 editor 流 | 纯文本行 | 高度不同步（原 bug） | 弃 |

## 非目标

- 不做批量删除、不做撤销
- 不改 QuestionForm/activeInline 框架本身（复用）
- 不做 sub-agent/远程 workspace 特殊处理（client_workspace 已实现 DeleteTurn）
- 不重做 offset/selection 修复（那是方案 B 的并发症，方案 A 无此问题）

## 用户故事

### US-D1: 恢复数据层与 UI 删除链（Phase 0 契约 + 骨架）

**契约**：
- 恢复 `Chat.SelectedMessageID()`（chat.go）
- 恢复 `km.Chat.DeleteTurn` 键位（keys.go）
- 恢复 `ui.go` 的 DeletedEvent 分支（RemoveMessage 消息本体 + tool calls + info footer）
- 恢复 message 层 DeleteTurn + workspace 接口（**已存在**，仅确认签名）

**验证门 1**：
1. `go build ./...` 通过 ✅
2. `SelectedMessageID()` 单测：选中消息返回其 MessageID，未选中返回 "" ✅

**提交**：数据链恢复。

### US-D2: QuestionForm 确认对话框（Phase 1 骨架 + Phase 2 主干）

**契约**：
- 新增 `openDeleteTurnConfirm()`：构造单问题 batch（TypeYesNo，Text="Delete this turn?"），`dialog.NewQuestionForm`，`OnAnswer` 检查 `Yes==true` 后调 `Workspace.DeleteTurn`，`OnCancel` 仅关闭
- 复用 `activeInline` 机制（不重复造轮子），处理 focus：`m.textarea.Blur()` + `m.focus = uiFocusEditor` + `m.activeInline.SetFocused(true)` + `updateLayoutAndSize()`
- `handleKeyPressMsg` 的 DeleteTurn 分支：校验 session/busy/选中 → `openDeleteTurnConfirm()`
- 注意：QuestionForm 的 `OnAnswer` 签名是 `[]question.Answer`，取 `[0].Yes`

**验证门 2**：
1. 编译 + 相关测试通过 ✅
2. 手动（或测试驱动）：ctrl+x → editor 区出现 "Delete this turn?" 表单，y 确认 → turn 删除，esc 取消 → 表单关闭 ✅
3. 布局验证：表单激活时 editor 高度 = `activeInline.Height()`（无内容超高）✅

**提交**：确认对话框可用。

### US-D3: 防回归测试（Phase 5 回归）

**契约**：
- `chat_test`：`SelectedMessageID` 覆盖（选中/未选中/tool item 映射）
- UI 层测试：DeleteTurn 键触发 → `deleteTurnConfirm` 状态/activeInline 激活；OnAnswer yes → DeleteTurn 被调用；OnCancel → 仅关闭
- 回归：message 包 `TestDeleteTurn`（现有，`message_test.go:701`）保持绿
- editor 布局：渲染 editor view 时无 confirmView 行（验证方案 A 不再改 renderEditorView）

**验证门 3**：相关包测试全绿 ✅

**提交**：测试。

### US-D4: E2E 人工校验（用户执行）

1. 会话中选中一条消息，按 ctrl+x → editor 区出现 yes/no 表单，默认 No ✅
2. 按 y → 整个 turn（user + assistant + tool 消息）删除，UI 同步移除 ✅
3. 重复步骤按 esc → 表单关闭、无删除 ✅
4. 删除中间 turn 后光标/editor 不错位（方案 A 核心目标）✅
5. 删除后滚动位置合理（不跳顶/不跳底异常）✅
6. 日志无 panic ✅
7. 全部通过 → push；任一失败 → 回对应 Phase 修复 ✅

## 提交计划

1. US-D1 通过 → 提交（数据链恢复）
2. US-D2 通过 → 提交（QuestionForm 确认）
3. US-D3 通过 → 提交（测试）
4. US-D4 用户校验 → push

## 参考

- 原功能提交：`6f1bb124`（可 `git show` 参考 message/workspace 层实现）
- QuestionForm 现有入口：`openBatchFormDialog`（ui.go:4603）
- activeInline 按键路由：ui.go:2547；布局高度：ui.go:3540
- YesNo 组件：`internal/ui/dialog/question_yesno.go`
- DeleteTurn 数据链：`internal/workspace/app_workspace.go:108`、`internal/message/message.go:233`
- question 类型：`internal/question/question.go`（TypeYesNo / Answer.Yes / Request）
