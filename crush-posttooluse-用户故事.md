# PostToolUse Hook 用户故事拆解（skeleton-first-coding）

## 背景

crush 目前只支持 1 个 hook 事件 `PreToolUse`（工具调用前门禁/改写）。后置触发 `PostToolUse`（工具执行完成后触发）缺失，导致：
- 无法感知工具执行**结果**（输出内容、错误），只能靠模型自觉审查
- 无法做"改代码即自动验证"（gofmt/test）等副作用收尾
- 回合结束通知（Stop 类）更无从谈起

本次只做 `PostToolUse`，不做 `Stop`/`Notification`/`UserPromptSubmit`（YAGNI，等真实需求）。

## 技术事实（已核实）

- 事件常量仅 `EventPreToolUse = "PreToolUse"`：`internal/hooks/hooks.go:15`
- `Runner.Run(ctx, eventName, sessionID, toolName, toolInputJSON)` **已按 event 参数化**：`internal/hooks/runner.go:90` —— 加新事件**不需要改 Runner**
- `matchingHooks(toolName)` 只按 matcher 匹配工具名、**不区分事件**：`runner.go:146` —— 同一 Runner 实例里混两个事件的 hook 会全部触发，因此 **PostToolUse 需要独立的 Runner 实例**
- 配置结构 `Config.Hooks map[string][]HookConfig`（`internal/config/config.go:666`）**天然按事件名索引**，`crushrc`/`crush.json` 里写 `hooks["PostToolUse"] = [...]` 即可，**config 结构零改动**
- 调用点 `hookedTool.Run`：`internal/agent/hooked_tool.go:54-100`
  - PreToolUse 在 `h.inner.Run` 之前（:56）
  - **PostToolUse 插在 `h.inner.Run` 之后**（:86 之后），此时 `resp`/`err` 已拿到
- coordinator 构建 runner 处：`internal/agent/coordinator.go:707-711` 只读 `Hooks[EventPreToolUse]`；包装点 `wrapToolsWithHooks(filteredTools, hookRunner, isSubAgent)`：`coordinator.go:810`
- `wrapToolsWithHooks`：`hooked_tool.go:31-40`，`isSubAgent=true` 跳过（子 agent 不触发 hooks，保持语义）
- payload 结构 `Payload{Event, SessionID, CWD, ToolName, ToolInput}`：`internal/hooks/input.go:23-29` —— **需加 `ToolResponse` 字段**
- 工具返回类型 `fantasy.ToolResponse{Content, Metadata, StopTurn}`（v0.41.2 tool.go）
- FUTURE.md 已警告 PostToolUse 的复杂度陷阱：输出清洗/错误强制/大小限制都是兔子洞（`docs/hooks/FUTURE.md:163`）—— **本次不做**

## 语义定义（与 PreToolUse 的差异）

| 输出字段 | PreToolUse | PostToolUse |
|---------|-----------|-------------|
| `decision: deny` | 阻止工具调用 | **无效**（工具已执行完），忽略并记日志 |
| `halt`（退出码 49） | 停止整个回合 | **保留**，停止回合 |
| `context` | 追加到工具结果后喂回模型 | **保留**，追加到工具结果后喂回模型 |
| `updated_input` | 改写工具入参 | **无效**（入参已用），忽略 |
| 退出码 2（block） | 阻止本次调用 | **无效**，视为非阻塞错误 |
| `tool_response`（新增入参） | 无 | 工具输出 JSON：content/error/metadata |

## 行为差异表（PostToolUse vs 现状）

| 场景 | 现状（无 PostToolUse） | 实现后 |
|------|----------------------|--------|
| 写文件后自动验证 | 靠模型自觉，或 agent 再调 bash | hook 退出码 0 + stdout context 自动追加回结果 |
| 工具输出有错误 | 模型自己发现 | hook 审查输出后 context 注入，模型下一轮必然看到 |
| 回合级收尾 | 无 | `halt` 可中断（但 Stop 事件不做） |
| hook 本身出错 | — | 非阻塞警告，不影响工具结果（与 PreToolUse 一致） |

## 非目标 / 开放问题

- **输出清洗/截断**（超大 tool_response）：不做，FUTURE.md 已标记兔子洞
- **错误强制/重试**（工具失败自动重跑）：不做
- **Stop / Notification / UserPromptSubmit 事件**：不做
- **sub-agent 触发 hooks**：维持现状（isSubAgent 跳过）
- **Runner 重构**（事件过滤）：两个独立 Runner 实例足够，等出现第三个事件再考虑

## 用户故事

### US-P1: 事件常量 + 调用点骨架（Phase 0+1）

**契约**：
- `internal/hooks/hooks.go` 新增 `EventPostToolUse = "PostToolUse"`
- `hookedTool` 增加 `postRunner *hooks.Runner` 字段
- `wrapToolsWithHooks` 签名改为接收 pre/post 两个 runner
- `hookedTool.Run` 在 `h.inner.Run` 之后调用 `postRunner.Run(ctx, EventPostToolUse, ...)`（先传空 tool_response），结果先丢弃，只打日志

**验证门 1**：
1. `go build ./...` 通过 ✅
2. 配置 `hooks["PostToolUse"]=[{command:"echo post ran >&2"}]`，真实会话里触发任意工具，日志出现 post 执行记录 ✅

**提交**：skeleton 可跑通状态。

### US-P2: payload 扩展 tool_response（Phase 2 主干）

**契约**：
- `Payload` 增加 `ToolResponse json.RawMessage \`json:"tool_response,omitempty"\``
- `BuildPayload` 增加可选参数（或新函数 `BuildPostToolUsePayload`）传入工具输出 JSON
- `hookedTool.Run` 组装 `tool_response`：`{content, error, metadata}`（content 截断上限先定 200KB，超限省略并标记——此项若实现复杂则延后到 Phase 3）
- PostToolUse 结果处理：`halt` → `resp.StopTurn = true` + metadata；`context` → 追加到 `resp.Content`

**验证门 2**：
1. hook `echo '{"context":"post-hook saw output"}'`，工具结果中出现该 context ✅
2. hook 退出码 49，回合停止 ✅
3. `decision: deny`（退出码 2）被忽略、工具结果不受影响 ✅

**提交**：主干可用。

### US-P3: coordinator 接线 + 配置生效（Phase 2 收尾）

**契约**：
- `coordinator.go:707` 同时读 `Hooks[EventPostToolUse]` 构建 `postRunner`
- 传入 `wrapToolsWithHooks`
- `crushrc`/`crush.json` 配置 `hooks["PostToolUse"]` 真实生效

**验证门 3**：
1. crushrc 写 `hooks PostToolUse=[{matcher:"write", command:"gofmt -w ..."}]`，真实会话中 `write` 工具执行后自动格式化 ✅
2. 未配置 PostToolUse 时零额外开销（无 hook 时 `matchingHooks` 提前返回，runner.go:92）✅

**提交**：端到端可用。

### US-P4: 测试 + 文档 + E2E 人工校验（Phase 5）

**契约**：
- 单测：
  - `input_test.go`：`BuildPayload` tool_response 字段序列化
  - `hooked_tool_test.go`：PostToolUse 触发（context 追加 / halt / deny 忽略 / 无 hook 零开销 / hook 出错不阻断）
  - `hooks_test.go`：退出码 2 在 post 语义下视为非阻塞（若 aggregate 层需区分事件则在此处理）
- 文档：`docs/hooks/README.md` 加 PostToolUse 章节；`FUTURE.md` 状态从"仅提及"更新为"已实现"
- 回归：现有 PreToolUse 全部测试保持绿

**E2E 主流程人工校验清单**（用户执行）：
1. 真实会话中配置一个 PostToolUse hook（如 `write` 后自动跑 `go vet`），执行一次写文件，确认验证输出出现在工具结果里 ✅
2. 配置 halt 场景（退出码 49），确认回合被中断且 UI 显示原因 ✅
3. 跑一次完整对话（含 PreToolUse 的 deny 场景），确认 PreToolUse 行为无回归 ✅
4. 检查日志无脏数据/panic ✅
5. 全部通过 → 提交；任一失败 → 回对应 Phase 修复重跑 ✅

## 提交计划（小步）

1. US-P1 通过 → 提交（事件常量 + 骨架调用点）
2. US-P2 通过 → 提交（payload + 结果处理）
3. US-P3 通过 → 提交（coordinator 接线）
4. US-P4 通过 → 提交（测试 + 文档）

## 参考

- 现有 PreToolUse 全链路：`internal/agent/hooked_tool.go:54-100` → `internal/hooks/runner.go:90-142` → `internal/hooks/input.go:32-76` → `shell.Run`
- 配置样例（crushrc Bash 语法）：
  ```
  hooks PostToolUse=[
    {name="vet-after-write", matcher="write|edit|multiedit", command="go vet ./... 2>&1", timeout=60}
  ]
  ```
