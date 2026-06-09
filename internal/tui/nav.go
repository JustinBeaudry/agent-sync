package tui

import tea "github.com/charmbracelet/bubbletea"

// PushMsg asks the navigator to push a new screen onto the stack. A screen
// returns it as a command (tui.Push(next)) to advance the wizard.
type PushMsg struct{ Screen tea.Model }

// PopMsg asks the navigator to pop the current screen, returning to the
// previous one. Popping the last screen quits the program.
type PopMsg struct{}

// Push returns a command that pushes screen onto the navigator stack.
func Push(screen tea.Model) tea.Cmd {
	return func() tea.Msg { return PushMsg{Screen: screen} }
}

// Pop returns a command that pops the current screen.
func Pop() tea.Cmd {
	return func() tea.Msg { return PopMsg{} }
}

// Nav is a stack navigator: it holds a stack of screen models, delegates
// Init/Update/View to the top screen, and interprets PushMsg/PopMsg to
// move between screens. When the stack empties, the program quits.
//
// Screens are ordinary tea.Models. They drive transitions by returning
// Push/Pop commands; they never manipulate the stack directly. This keeps
// each screen independently testable.
type Nav struct {
	stack []tea.Model
}

// NewNav builds a navigator whose initial screen is root.
func NewNav(root tea.Model) *Nav {
	return &Nav{stack: []tea.Model{root}}
}

// Depth reports the number of screens on the stack (test/observability).
func (n *Nav) Depth() int { return len(n.stack) }

// Top returns the current screen, or nil when the stack is empty.
func (n *Nav) Top() tea.Model {
	if len(n.stack) == 0 {
		return nil
	}
	return n.stack[len(n.stack)-1]
}

// Init initializes the top screen.
func (n *Nav) Init() tea.Cmd {
	if top := n.Top(); top != nil {
		return top.Init()
	}
	return nil
}

// Update routes the message to the top screen, then interprets any
// navigation message the screen emitted. Push/Pop messages mutate the
// stack; the new top screen is initialized on push.
func (n *Nav) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case PushMsg:
		n.stack = append(n.stack, m.Screen)
		return n, m.Screen.Init()
	case PopMsg:
		if len(n.stack) > 0 {
			n.stack = n.stack[:len(n.stack)-1]
		}
		if len(n.stack) == 0 {
			return n, tea.Quit
		}
		return n, nil
	}

	top := n.Top()
	if top == nil {
		return n, tea.Quit
	}
	updated, cmd := top.Update(msg)
	n.stack[len(n.stack)-1] = updated
	return n, cmd
}

// View renders the top screen.
func (n *Nav) View() string {
	if top := n.Top(); top != nil {
		return top.View()
	}
	return ""
}
