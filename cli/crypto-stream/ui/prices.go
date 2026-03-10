package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/uwezukwechibuzor/termino/pkg/models"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	greenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	redStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#AAAA00")).
			Padding(0, 1)
)

type priceMsg models.AggregatedPrice
type errMsg struct{ err error }
type tickMsg time.Time

type priceModel struct {
	table     table.Model
	prices    map[string]models.AggregatedPrice
	symbols   []string
	mu        sync.Mutex
	serverURL string
	err       error
	connected bool
	lastUpdate time.Time
}

func RunPriceStream(serverURL string, symbols []string) {
	columns := []table.Column{
		{Title: "SYMBOL", Width: 8},
		{Title: "PRICE", Width: 14},
		{Title: "AVG", Width: 14},
		{Title: "HIGH", Width: 14},
		{Title: "LOW", Width: 14},
		{Title: "CHANGE %", Width: 10},
		{Title: "VOLUME", Width: 14},
		{Title: "UPDATED", Width: 10},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(len(symbols)+1),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57"))
	t.SetStyles(s)

	m := &priceModel{
		table:     t,
		prices:    make(map[string]models.AggregatedPrice),
		symbols:   symbols,
		serverURL: serverURL,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func (m *priceModel) Init() tea.Cmd {
	return tea.Batch(
		m.connectSSE(),
		m.tickCmd(),
	)
}

func (m *priceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}

	case priceMsg:
		m.mu.Lock()
		p := models.AggregatedPrice(msg)
		m.prices[p.Symbol] = p
		m.lastUpdate = time.Now()
		m.connected = true
		m.mu.Unlock()
		m.updateTable()

	case errMsg:
		m.err = msg.err
		m.connected = false
		// Retry connection
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case tickMsg:
		if !m.connected {
			return m, m.connectSSE()
		}
		return m, m.tickCmd()
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *priceModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(" CRYPTO PRICE STREAM "))
	b.WriteString("\n\n")

	if m.err != nil && !m.connected {
		b.WriteString(redStyle.Render(fmt.Sprintf("  Connection error: %v (retrying...)", m.err)))
		b.WriteString("\n\n")
	}

	b.WriteString(m.table.View())
	b.WriteString("\n")

	status := fmt.Sprintf("Symbols: %s", strings.Join(m.symbols, ", "))
	if !m.lastUpdate.IsZero() {
		status += fmt.Sprintf(" | Last update: %s", m.lastUpdate.Format("15:04:05"))
	}
	if m.connected {
		status += " | " + greenStyle.Render("● CONNECTED")
	} else {
		status += " | " + redStyle.Render("● DISCONNECTED")
	}

	b.WriteString(statusStyle.Render(status))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Press q to quit"))

	return b.String()
}

func (m *priceModel) updateTable() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Sort symbols for stable display
	syms := make([]string, 0, len(m.symbols))
	for _, s := range m.symbols {
		syms = append(syms, s)
	}
	sort.Strings(syms)

	rows := make([]table.Row, 0, len(syms))
	for _, sym := range syms {
		p, ok := m.prices[sym]
		if !ok {
			rows = append(rows, table.Row{sym, "---", "---", "---", "---", "---", "---", "---"})
			continue
		}

		changeStr := fmt.Sprintf("%.2f%%", p.ChangePct)
		if p.ChangePct > 0 {
			changeStr = greenStyle.Render("+" + changeStr)
		} else if p.ChangePct < 0 {
			changeStr = redStyle.Render(changeStr)
		}

		updated := time.Unix(p.Timestamp, 0).Format("15:04:05")

		rows = append(rows, table.Row{
			sym,
			formatPrice(p.Price),
			formatPrice(p.AvgPrice),
			formatPrice(p.HighPrice),
			formatPrice(p.LowPrice),
			changeStr,
			formatVolume(p.Volume),
			updated,
		})
	}

	m.table.SetRows(rows)
}

func (m *priceModel) connectSSE() tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/stream?symbols=%s", m.serverURL, strings.Join(m.symbols, ","))

		resp, err := http.Get(url)
		if err != nil {
			return errMsg{err}
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			var price models.AggregatedPrice
			if err := json.Unmarshal([]byte(data), &price); err != nil {
				continue
			}

			return priceMsg(price)
		}

		if err := scanner.Err(); err != nil {
			return errMsg{err}
		}

		return errMsg{fmt.Errorf("stream closed")}
	}
}

func (m *priceModel) tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func formatPrice(p float64) string {
	if p >= 1000 {
		return fmt.Sprintf("$%.2f", p)
	} else if p >= 1 {
		return fmt.Sprintf("$%.4f", p)
	}
	return fmt.Sprintf("$%.6f", p)
}

func formatVolume(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.2fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.2fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.2fK", v/1e3)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}
