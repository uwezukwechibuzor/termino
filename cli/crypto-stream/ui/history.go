package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/uwezukwechibuzor/termino/pkg/models"
)

type historyMsg struct {
	prices []models.AggregatedPrice
}

type historyModel struct {
	table     table.Model
	symbol    string
	serverURL string
	prices    []models.AggregatedPrice
	err       error
}

func RunHistory(serverURL string, symbol string) {
	columns := []table.Column{
		{Title: "TIME", Width: 18},
		{Title: "PRICE", Width: 14},
		{Title: "AVG", Width: 14},
		{Title: "HIGH", Width: 14},
		{Title: "LOW", Width: 14},
		{Title: "CHANGE %", Width: 10},
		{Title: "VOLUME", Width: 14},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(20),
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

	m := &historyModel{
		table:     t,
		symbol:    symbol,
		serverURL: serverURL,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func (m *historyModel) Init() tea.Cmd {
	return m.fetchHistory()
}

func (m *historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" || msg.String() == "esc" {
			return m, tea.Quit
		}
		if msg.String() == "r" {
			return m, m.fetchHistory()
		}

	case historyMsg:
		m.prices = msg.prices
		m.err = nil
		rows := make([]table.Row, 0, len(msg.prices))
		for _, p := range msg.prices {
			ts := time.Unix(p.Timestamp, 0).Format("2006-01-02 15:04")
			changeStr := fmt.Sprintf("%.2f%%", p.ChangePct)
			rows = append(rows, table.Row{
				ts,
				formatPrice(p.Price),
				formatPrice(p.AvgPrice),
				formatPrice(p.HighPrice),
				formatPrice(p.LowPrice),
				changeStr,
				formatVolume(p.Volume),
			})
		}
		m.table.SetRows(rows)

	case errMsg:
		m.err = msg.err
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *historyModel) View() string {
	s := titleStyle.Render(fmt.Sprintf(" %s HISTORY ", m.symbol)) + "\n\n"

	if m.err != nil {
		s += redStyle.Render(fmt.Sprintf("  Error: %v\n\n", m.err))
	}

	// Sparkline chart of price history
	if len(m.prices) > 1 {
		// Reverse so oldest is first for sparkline
		data := make([]float64, len(m.prices))
		for i, p := range m.prices {
			data[len(m.prices)-1-i] = p.Price
		}

		min, max := data[0], data[0]
		for _, v := range data {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}

		s += fmt.Sprintf("  Price Range: %s - %s (%d points)\n",
			formatPrice(min), formatPrice(max), len(data))
		s += "  " + renderSparkline(data) + "\n\n"
	}

	s += m.table.View() + "\n"

	// Summary stats
	if len(m.prices) > 0 {
		latest := m.prices[0]
		oldest := m.prices[len(m.prices)-1]
		s += "\n"
		s += dimStyle.Render(fmt.Sprintf("  Latest: %s | Oldest: %s | Records: %d",
			formatPrice(latest.Price), formatPrice(oldest.Price), len(m.prices)))
		s += "\n"
	}

	s += dimStyle.Render("  Press r to refresh, q to quit\n")
	return s
}

func (m *historyModel) fetchHistory() tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 10 * time.Second}
		url := fmt.Sprintf("%s/history?symbol=%s&limit=50", m.serverURL, m.symbol)
		resp, err := client.Get(url)
		if err != nil {
			return errMsg{err}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return errMsg{fmt.Errorf("server returned %d", resp.StatusCode)}
		}

		var prices []models.AggregatedPrice
		if err := json.NewDecoder(resp.Body).Decode(&prices); err != nil {
			return errMsg{err}
		}

		return historyMsg{prices: prices}
	}
}
