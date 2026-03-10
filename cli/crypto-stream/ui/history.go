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
	prices map[string]models.AggregatedPrice
}

type historyModel struct {
	table     table.Model
	symbol    string
	serverURL string
	err       error
}

func RunHistory(serverURL string, symbol string) {
	columns := []table.Column{
		{Title: "SYMBOL", Width: 8},
		{Title: "PRICE", Width: 14},
		{Title: "AVG", Width: 14},
		{Title: "HIGH", Width: 14},
		{Title: "LOW", Width: 14},
		{Title: "CHANGE %", Width: 10},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	t.SetStyles(s)

	m := &historyModel{
		table:     t,
		symbol:    symbol,
		serverURL: serverURL,
	}

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func (m *historyModel) Init() tea.Cmd {
	return m.fetchLatest()
}

func (m *historyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.String() == "r" {
			return m, m.fetchLatest()
		}

	case historyMsg:
		rows := make([]table.Row, 0)
		if p, ok := msg.prices[m.symbol]; ok {
			changeStr := fmt.Sprintf("%.2f%%", p.ChangePct)
			rows = append(rows, table.Row{
				p.Symbol,
				formatPrice(p.Price),
				formatPrice(p.AvgPrice),
				formatPrice(p.HighPrice),
				formatPrice(p.LowPrice),
				changeStr,
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

	s += m.table.View() + "\n"
	s += dimStyle.Render("  Press r to refresh, q to quit\n")
	return s
}

func (m *historyModel) fetchLatest() tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(m.serverURL + "/prices")
		if err != nil {
			return errMsg{err}
		}
		defer resp.Body.Close()

		var prices map[string]models.AggregatedPrice
		if err := json.NewDecoder(resp.Body).Decode(&prices); err != nil {
			return errMsg{err}
		}

		return historyMsg{prices: prices}
	}
}
