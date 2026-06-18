package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			Bold(true)

	descStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)

type model struct {
	choice   int
	choices  []string
	cursor   int
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.choice = m.cursor
		}
	}
	return m, nil
}

func (m model) View() string {
	s := titleStyle.Render("Select an option:") + "\n\n"

	for i, choice := range m.choices {
		cursor := "  "
		if m.cursor == i {
			cursor = "> "
		}

		selected := ""
		if m.choice == i {
			selected = " (selected)"
		}

		s += descStyle.Render(cursor+choice+selected) + "\n"
	}

	return s
}

func main() {
	m := model{
		choices: []string{"Option 1", "Option 2", "Option 3"},
	}

	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error:", err)
	}
}
