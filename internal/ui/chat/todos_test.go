package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestFormatTodosList_Tree(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	todos := []session.Todo{
		{Content: "root", Status: session.TodoStatusPending},
		{Content: "child", Status: session.TodoStatusPending, Parent: "root"},
		{Content: "grandchild", Status: session.TodoStatusPending, Parent: "child"},
		{Content: "sibling", Status: session.TodoStatusPending},
	}

	got := ansi.Strip(FormatTodosList(&sty, todos, ">", 80))
	want := styles.TodoPendingIcon + " root\n" +
		todoIndentUnit + styles.TodoPendingIcon + " child\n" +
		todoIndentUnit + todoIndentUnit + styles.TodoPendingIcon + " grandchild\n" +
		styles.TodoPendingIcon + " sibling"
	require.Equal(t, want, got)
}

// TestFormatTodosList_SiblingsSortWithoutScatteringTree pins the trap called
// out in the design: status sorting must stay within a sibling group so a
// completed child never floats above its still-pending parent.
func TestFormatTodosList_SiblingsSortWithoutScatteringTree(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	todos := []session.Todo{
		{Content: "parent", Status: session.TodoStatusPending},
		{Content: "todo-child", Status: session.TodoStatusPending, Parent: "parent"},
		{Content: "done-child", Status: session.TodoStatusCompleted, Parent: "parent"},
		{Content: "done-root", Status: session.TodoStatusCompleted},
	}

	got := ansi.Strip(FormatTodosList(&sty, todos, ">", 80))
	want := styles.TodoCompletedIcon + " done-root\n" +
		styles.TodoPendingIcon + " parent\n" +
		todoIndentUnit + styles.TodoCompletedIcon + " done-child\n" +
		todoIndentUnit + styles.TodoPendingIcon + " todo-child"
	require.Equal(t, want, got)
}

func TestOrderedTodosKeepsCyclicNodes(t *testing.T) {
	t.Parallel()

	todos := []session.Todo{
		{Content: "a", Parent: "b"},
		{Content: "b", Parent: "a"},
	}
	require.Len(t, orderedTodos(todos), 2)
}

func TestOrderedTodosOrphansBecomeRoots(t *testing.T) {
	t.Parallel()

	todos := []session.Todo{
		{Content: "root"},
		{Content: "orphan", Parent: "ghost"},
	}
	ordered := orderedTodos(todos)
	require.Len(t, ordered, 2)
	require.Equal(t, "orphan", ordered[1].Content)
}
