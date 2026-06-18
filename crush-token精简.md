# Crush Token 优化方案

基于对 Crush 代码库（`internal/agent/templates/coder.md.tpl` ~420 行 system prompt 模板、`internal/skills/` skill 注入机制、`internal/agent/agent.go` token 预算逻辑、`internal/client/proto.go` 重复模式等）和完整 prompt 流程的深入分析。

---

## 输入侧（System Prompt + Context）

### 1. 系统模板精简（`coder.md.tpl`, ~420 行 → 目标 ~250 行）

当前模板有以下可精简部分：

| 章节 | 行数 | 问题 | 方案 |
|------|------|------|------|
| `<editing_files>` | ~38 行 | 与 `<whitespace_and_exact_matching>` 高度重复（都讲"精确匹配"、"注意空格"、"失败重读"） | 合并为 `<editing_rules>`，砍掉 ~30 行 |
| `<whitespace_and_exact_matching>` | ~28 行 | 同上，超过 60% 内容与 editing_files 重复 | 合并 |
| `<error_handling>` | ~24 行 | "Edit tool old_string not found" 部分也是 editing 的子集 | 精简到 10 行 |
| 示例代码 | ~20 行 | `user: what is 2+2? / assistant: 4` 等 6 个示例 | 强模型不需要这么多示例，留 2 个最典型的 |
| `<communication_style>` 解释 | 多处 | "Conciseness is about **text only**…" 等解释性文字 | 砍半，保留规则即可 |
| `<workflow>` | ~37 行 | "Before acting / While acting / Before finishing" 三个阶段，每段前都有加粗标题 | 每段保留 2-3 条核心规则 |

**预估节省：10-20% 系统 prompt volume**

注意：当前模板硬编码了 Claude/Claude Code 风格的规则。如果目标是不同 provider（如 DeepSeek、OpenAI），部分规则不适用，可做条件渲染。

---

### 2. Skills XML 懒加载（替代全量注入）

**现状**：每个 session 把所有已发现的 skill 生成 `<available_skills>` XML 区块，直接嵌入 system prompt。

`internal/skills/skills.go:297-322` 的 `ToPromptXML()` 对每个 skill 输出：
```xml
<skill>
  <name>xxx</name>
  <description>yyyy</description>
  <location>/path/to/SKILL.md</location>
</skill>
```

在当前 repo 有 20+ skill（内置 + 用户自定义）的场景下，每个 skill 占 3-5 行，合计 100-300+ tokens 的固定开销。每次对话、每个 turn 都重复发送。

**方案**：
- system prompt 只保留一行提示 + 一个 `list_skills` 工具
- 模型需要时调用工具按需加载 skill 列表
- 或只在首次对话时注入，标记 `skills_injected=true` 后跳过

**预估节省：100-300+ tok/session**，skill 越多效果越明显。

---

### 3. 上下文文件按需加载

**现状**：
- `ContextPaths` 自动匹配并加载 `AGENTS.md`、`CLAUDE.md`、`crush.md`、`.cursorrules` 等
- 加载后全量注入 system prompt 的 `<project_context>` 区块
- 某些文件（如 `AGENTS.md` 800+ 行）体积很大

**方案**：
- 引入 token 预算：只注入总大小 ≤ 2K tokens 的文件
- 超大文件提供 `read_context_file` 工具让模型按需读取
- 在 `internal/agent/prompt/prompt.go` 的 `promptData()` 中实现大小过滤

---

### 4. MCP Instructions 缓存

**现状**（`agent.go` ~line 639-670）：
```go
for _, server := range mcp.GetStates() {
    if s := server.Client.InitializeResult().Instructions; s != "" {
        instructions.WriteString(s)
    }
}
systemPrompt += "\n\n<mcp-instructions>\n" + s + "\n</mcp-instructions>"
```

每个 turn 都会重新 append 所有 MCP 的 instructions。

**方案**：
- 首次连接后缓存到 `session` 对象
- 后续仅注入 `<mcp-instructions cached="true" hash="xxx"/>` 标记
- 模型只需要在首次看到完整内容

---

### 5. `schema.json` 删除

**现状**：`schema.json`（819 行）是通过 Go struct 的 `jsonschema` 标签自动生成的 JSON Schema 文件。与 `internal/config/config.go` 中的 struct tags 完全重复。

**方案**：
- 删掉该文件
- 在 `go:generate` 指令中按需生成（`//go:generate go run github.com/invopop/jsonschema/cmd/...`）

**预估节省：819 行仓库空间，减少克隆/构建时的 token 开销**

---

### 6. 总结阈值调低

**现状**（`agent.go:54-56`）：
```go
largeContextWindowThreshold = 200_000
largeContextWindowBuffer    = 20_000
smallContextWindowRatio     = 0.2
```

只有 >200K 的模型窗口才有 20K buffer；<200K 的模型到 80% 才触发自动总结。这意味着很多 token 花在 message history 上。

**方案**：
- 改为可配置项（`crush.json` 或环境变量）
- 默认值 `largeContextWindowBuffer` 从 20K → 10K（更激进地触发总结）
- 或用户自定义 `session_summary_threshold`

---

### 7. 工具描述瘦身

**现状**：
- `coordinator.go` 中所有 tool 的描述、参数 schema 直接透传给 LLM
- 某些工具描述为了可读性写了很长的自然语言

**方案**：
- 审查 `internal/agent/` 中注册的所有 tool 描述
- 保持功能不变的前提下，砍掉冗余副词和解释性语句
- 统一使用 `<3 行的简短描述

---

## 输出侧

### 8. 默认 `MaxTokens` 降低

**现状**（`coordinator.go:214-218`）：
```go
maxTokens := model.CatwalkCfg.DefaultMaxTokens  // 通常是 4096-8192
if model.ModelCfg.MaxTokens != 0 {
    maxTokens = model.ModelCfg.MaxTokens
}
```

**方案**：
- `crush.json` 增加全局 `default_max_output_tokens: 2048`
- 用户或任务需要时再覆盖
- 减少模型输出长篇回答的倾向

---

### 9. Output 指令强化

**现状**：system prompt 中说 "Keep output concise (default <4 lines)"，但只是建议性语言。

**方案**：
- 改为硬性约束："每条消息正文不超过 200 tokens，除非用户明确要求详述"
- 或实现 output budget 跟踪：累计 output tokens 超过阈值后自动压缩后续回答
- 在 `<communication_style>` 中增加更明确的输出长度上限

---

## 代码层面的 token 浪费

### 10. 重复错误处理模式（`internal/client/proto.go`）

**现状**：35 个方法每个都重复相同的 4 行 error handling 样板：
```go
rsp, err := c.get(ctx, "/path", nil, nil)
if err != nil {
    return nil, fmt.Errorf("failed to do X: %w", err)
}
defer rsp.Body.Close()
if rsp.StatusCode != http.StatusOK {
    return nil, fmt.Errorf("failed to do X: status code %d", rsp.StatusCode)
}
var result ProtoType
if err := json.NewDecoder(rsp.Body).Decode(&result); err != nil {
    return nil, fmt.Errorf("failed to decode X: %w", err)
}
```

**方案**：抽出 `doRequest` helper 函数，处理 get/post 请求的公共流程。每个方法缩减到 5-8 行。

**预估节省：~70% 代码体积（该文件从 843 行 → ~300 行）**

---

### 11. Provider 工厂重复（`internal/agent/coordinator.go`）

**现状**：9 个近乎相同的 `buildXxxProvider` 方法（Anthropic、OpenAI、OpenRouter、Vercel、OpenAICompat、Azure、Bedrock、Google、GoogleVertex），每个 30-60 行，大量重复。

**方案**：
- provider registry map: `map[string]ProviderBuilder`
- 每个 provider 只保留差异配置（base URL、auth header 格式等）
- 统一 builder 函数处理 90% 的公共逻辑

**预估节省：~500 行**

---

### 12. 自动生成的 Swagger（`internal/swagger/docs.go`）

**现状**：3717 行的纯 swagger JSON 字符串，是 `go-swagger` 生成的。

**方案**：
- 不提交到仓库
- 构建时 `go:generate` 生成
- `.gitignore` 忽略

---

## 实施优先级

| 优先级 | 方案 | 类型 | 预估节省 | 风险 | 工作量 |
|--------|------|------|---------|------|--------|
| P0 | 精简 `coder.md.tpl` | 输入 | 10-20% system prompt | 低 | 小 |
| P1 | Skill XML 懒加载 | 输入 | 100-300 tok/session | 中 | 中 |
| P1 | 删 `schema.json` | 代码 | 819 行冗余文件 | 低 | 小 |
| P1 | 输出指令强化 | 输出 | 10-30% 输出体积 | 低 | 小 |
| P2 | 总结阈值调低 | 输入 | 取决于使用模式 | 中 | 小 |
| P2 | MCP Instructions 缓存 | 输入 | 取决于 MCP 配置 | 低 | 中 |
| P2 | 上下文文件按需加载 | 输入 | 取决于项目 | 中 | 中 |
| P3 | client proto 重构 | 代码 | 开发效率 | 低 | 中 |
| P3 | provider builder 重构 | 代码 | 开发效率 | 低 | 大 |
| P3 | swagger 不提交 | 代码 | 3717 行 | 低 | 小 |

**最快见效**：P0 和 P1 中的模板精简 + 输出指令强化 + 删 schema，不需要改 Go 核心逻辑，改完立即生效。
