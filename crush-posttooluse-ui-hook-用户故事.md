# PostToolUse UI Hook 指示器修复 用户故事拆解（skeleton-first-coding）

## 背景

PostToolUse 功能已实现（4 个提交），hook 执行、context 注入、halt 均正常，但 **UI 上不显示 PostToolUse 的 hook 指示器**（PreToolUse 显示正常）。

## 根因分析（已核实）

### Bug 1：metadata merge 覆盖（代码缺陷）

`internal/agent/hooked_tool.go` 的 `Run` 中两次调用 `mergeHookMetadata`：
- `:117` PostToolUse 结果先 merge（`resp.Metadata = mergeHookMetadata(resp.Metadata, postResult)`）
- `:130` PreToolUse 结果后 merge（`resp.Metadata = mergeHookMetadata(resp.Metadata, result)`）

`mergeHookMetadata`（`hooked_tool.go:125-142`）用 `sjson.SetRaw(existing, "hook", ...)` **整体覆盖** `hook` 键。因此当 PreToolUse 与 PostToolUse 同时匹配同一工具（如 bash 同时有 rtk-hook 和 post hook）时，`:130` 的 PreToolUse merge 会把 `:117` 写入的 PostToolUse hook 信息覆盖掉，UI 只剩 PreToolUse 显示。

**复现**：`TestPostToolUse_MetadataBothRunners`（临时测试）证明双 runner 时 `METADATA` 只含 pre hook，post hook 丢失。

### 场景问题：matcher 不匹配

当前 crush.json PostToolUse 仅配置 `^view$`（view-chars.sh），用户测试的会话是 bash 命令 → PostToolUse 不触发 → UI 无显示。这是配置使然，非 bug。

## 技术事实（已核实）

- UI 渲染：`internal/ui/chat/tools.go:355` `toolOutputHookIndicator(t.sty, t.result.Metadata, ...)`，从 metadata 解析 `{"hook":{...}}`，遍历 `h.Hooks` 渲染每行 "Hook <name> → detail"
- UI 只读 `t.result.Metadata`（`message.ToolResult.Metadata`，`content.go:123`）
- metadata 全链路透传：hookedTool 返回 `resp.Metadata` → fantasy `result.ClientMetadata`（agent.go:833）→ `convertToToolResult`（agent.go:2095）→ `ToolResult.Metadata` → UI `SetResult`（ui.go:1657）
- `mergeHookMetadata` 覆盖语义在 `hooked_tool.go:137`（`sjson.SetRaw(existing, "hook", ...)`）
- `HookMetadata.HookCount` 是 int 累加值；`Hooks []HookInfo` 是数组
- UI 对 `len(h.Hooks) == 0` 返回空串（tools.go:782），单 hook 也能显示
- hookDetail 渲染 decision: "deny"/"allow"/"none"（tools.go:898-919）；PostToolUse 的 decision 恒为 "none"（忽略 deny），显示 "OK"

## 语义定义

| 场景 | 现状 | 修复后 |
|------|------|--------|
| 仅 PreToolUse 匹配 | 显示 Pre | 不变 |
| 仅 PostToolUse 匹配 | 显示 Post（单 hook 正常） | 不变 |
| Pre+Post 都匹配 | **Post 被覆盖，只显示 Pre** | **两者都显示**（合并） |

## 非目标

- 不改 UI 渲染结构（`toolOutputHookIndicator` 无需改动，合并后天然支持多行）
- 不加 Stop/Notification 事件
- 不改 PostToolUse 执行语义（context/halt/忽略 deny）

## 用户故事

### US-U1: merge 语义从覆盖改为合并（核心修复）

**契约**：
- 新增 `mergeHookMetadataMany(existing string, results ...hooks.AggregateResult) string`（或改 `mergeHookMetadata` 内部），把多次 merge 改为**累加合并**：
  - 若 existing 已含 `hook` 键：解析出已有 `HookMetadata`，把新结果的 `Hooks` 追加到数组末尾、`HookCount` 累加，写回
  - 若 existing 无 `hook` 键：行为同现在（SetRaw 新建）
- `hooked_tool.Run` 中 `:117`/`:130` 两次调用改为一次合并调用（post 先、pre 后，顺序=配置显示顺序）或保持两次但第二次是追加语义
- 保留 `mergeHookMetadata` 签名（兼容现有调用）或更新调用点

**验证门 1**：
1. `go build ./...` 通过 ✅
2. 新增测试：Pre+Post 都配置且都匹配 → `resp.Metadata` 解析后 `hook.hook_count == 2`，`hooks` 数组含两个 hook 名 ✅
3. 现有 `TestHookedTool_*` 全绿（单 runner 场景不回归）✅

**提交**：merge 修复。

### US-U2: UI 渲染单测（防回归）

**契约**：
- `internal/ui/chat/tools_test.go`（或现有渲染测试文件）新增：
  - 构造含两个 hook（pre+post）的 metadata JSON，`toolOutputHookIndicator` 输出两行，含两个 hook 名
  - 单 post hook metadata → 输出一行
  - 空 metadata → 空串

**验证门 2**：UI 渲染测试全绿 ✅

**提交**：UI 测试。

### US-U3: 配置场景验证 + 文档

**契约**：
- 检查 crush.json PostToolUse 是否覆盖用户实际测试场景（bash 会话需配 `^bash$` matcher 或去 matcher）
- `docs/hooks/README.md` 补一句：UI 同时显示 Pre/Post hook 指示器；PostToolUse 的 decision 恒显示 OK（deny 被忽略）
- 回归：`internal/hooks`、`internal/agent`（HookedTool 相关）、`internal/ui/chat` 测试全绿

**验证门 3**：
1. 文档更新 ✅
2. 相关包测试全绿 ✅

**提交**：文档 + 回归。

### US-U4: E2E 人工校验（用户执行）

1. 配置 Pre+Post 都匹配 bash 的 hook（如 PreToolUse rtk-hook + PostToolUse `echo '{"context":"post"}'`），跑一次 bash 工具，UI 工具卡片显示两行 hook ✅
2. 配置仅 PostToolUse `^view$`，跑一次 view 工具，UI 显示一行 hook + context ✅
3. 跑一次纯 PreToolUse 场景，确认无回归 ✅
4. 日志无 panic ✅
5. 全部通过 → push；任一失败 → 回对应 Phase 修复 ✅

## 提交计划

1. US-U1 通过 → 提交（merge 修复）
2. US-U2 通过 → 提交（UI 测试）
3. US-U3 通过 → 提交（文档 + 回归）

## 参考

- merge 覆盖点：`internal/agent/hooked_tool.go:117,130,137`
- UI 渲染：`internal/ui/chat/tools.go:355,767-840,898-919`
- metadata 结构：`internal/hooks/hooks.go:26-52`（HookMetadata/HookInfo）
- 测试基建：`newHookedTool(inner, preRunner, postRunner)`、`newPostRunner(t, cmd)`
