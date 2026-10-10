package tools

import (
	"context"
	_ "embed"
	"fmt"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/session"
)

//go:embed todos.md
var todosDescription string

const TodosToolName = "todos"

type TodosParams struct {
	Todos []TodoItem `json:"todos" description:"The updated todo list"`
}

type TodoItem struct {
	Content    string `json:"content" description:"What needs to be done (imperative form)"`
	Status     string `json:"status" description:"Task status: pending, in_progress, or completed"`
	ActiveForm string `json:"active_form" description:"Present continuous form (e.g., 'Running tests')"`
	Parent     string `json:"parent,omitempty" description:"Content of the parent todo for nesting; empty for a top-level task"`
}

type TodosResponseMetadata struct {
	IsNew         bool           `json:"is_new"`
	Todos         []session.Todo `json:"todos"`
	JustCompleted []string       `json:"just_completed,omitempty"`
	JustStarted   string         `json:"just_started,omitempty"`
	Completed     int            `json:"completed"`
	Total         int            `json:"total"`
}

func NewTodosTool(sessions session.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TodosToolName,
		todosDescription,
		func(ctx context.Context, params TodosParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for managing todos")
			}

			currentSession, err := sessions.Get(ctx, sessionID)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to get session: %w", err)
			}

			isNew := len(currentSession.Todos) == 0
			oldStatusByContent := make(map[string]session.TodoStatus)
			for _, todo := range currentSession.Todos {
				oldStatusByContent[todo.Content] = todo.Status
			}

			if err := validateTodos(params.Todos); err != nil {
				return fantasy.ToolResponse{}, err
			}

			todos := make([]session.Todo, len(params.Todos))
			var justCompleted []string
			var justStarted string
			completedCount := 0

			for i, item := range params.Todos {
				todos[i] = session.Todo{
					Content:    item.Content,
					Status:     session.TodoStatus(item.Status),
					ActiveForm: item.ActiveForm,
					Parent:     item.Parent,
				}

				newStatus := session.TodoStatus(item.Status)
				oldStatus, existed := oldStatusByContent[item.Content]

				if newStatus == session.TodoStatusCompleted {
					completedCount++
					if existed && oldStatus != session.TodoStatusCompleted {
						justCompleted = append(justCompleted, item.Content)
					}
				}

				if newStatus == session.TodoStatusInProgress {
					if !existed || oldStatus != session.TodoStatusInProgress {
						if item.ActiveForm != "" {
							justStarted = item.ActiveForm
						} else {
							justStarted = item.Content
						}
					}
				}
			}

			currentSession.Todos = todos
			_, err = sessions.Save(ctx, currentSession)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to save todos: %w", err)
			}

			response := "Todo list updated successfully.\n\n"

			pendingCount := 0
			inProgressCount := 0

			for _, todo := range todos {
				switch todo.Status {
				case session.TodoStatusPending:
					pendingCount++
				case session.TodoStatusInProgress:
					inProgressCount++
				}
			}

			response += fmt.Sprintf("Status: %d pending, %d in progress, %d completed\n",
				pendingCount, inProgressCount, completedCount)

			response += "Todos have been modified successfully. Ensure that you continue to use the todo list to track your progress. Please proceed with the current tasks if applicable."

			metadata := TodosResponseMetadata{
				IsNew:         isNew,
				Todos:         todos,
				JustCompleted: justCompleted,
				JustStarted:   justStarted,
				Completed:     completedCount,
				Total:         len(todos),
			}

			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(response), metadata), nil
		},
	)
}

// validateTodos 校验待办列表的合法性与树形结构（扁平列表 + parent 引用）。
//
// items
// 待写入的待办条目，含 content / status / active_form / parent。
//
// 核心流程
// 1. 校验 status 取值、content 非空且唯一（content 作为树形结构的键）
// 2. 校验 parent 非自引用且必须指向本批次内已存在的 content
// 3. 从每个节点向上回溯 parent 链，检测环引用
func validateTodos(items []TodoItem) error {
	contentSet := make(map[string]struct{}, len(items))
	for _, item := range items {
		switch item.Status {
		case "pending", "in_progress", "completed":
		default:
			return fmt.Errorf("invalid status %q for todo %q", item.Status, item.Content)
		}
		if item.Content == "" {
			return fmt.Errorf("todo content must not be empty")
		}
		if _, dup := contentSet[item.Content]; dup {
			return fmt.Errorf("duplicate todo content %q: content is used as the tree key", item.Content)
		}
		contentSet[item.Content] = struct{}{}
	}

	parentByContent := make(map[string]string, len(items))
	for _, item := range items {
		parentByContent[item.Content] = item.Parent
		if item.Parent == "" {
			continue
		}
		if item.Parent == item.Content {
			return fmt.Errorf("todo %q cannot be its own parent", item.Content)
		}
		if _, ok := contentSet[item.Parent]; !ok {
			return fmt.Errorf("todo %q references missing parent %q", item.Content, item.Parent)
		}
	}

	for _, item := range items {
		seen := make(map[string]struct{})
		for cur := item.Content; cur != ""; {
			if _, ok := seen[cur]; ok {
				return fmt.Errorf("cyclic parent chain detected at %q", cur)
			}
			seen[cur] = struct{}{}
			cur = parentByContent[cur]
		}
	}
	return nil
}
