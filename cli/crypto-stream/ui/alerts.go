package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/uwezukwechibuzor/termino/pkg/models"
)

var (
	alertTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#FF0000")).
			Padding(0, 2)

	alertBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#FF6600")).
			Padding(0, 1).
			Width(60)

	alertSymbolStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FF6600"))
)

type alertMsg models.PriceAlert

type alertModel struct {
	alerts    []models.PriceAlert
	serverURL string
	connected bool
	err       error
}

func RunAlertStream(serverURL string) {
	m := &alertModel{
		serverURL: serverURL,
		alerts:    make([]models.PriceAlert, 0),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func (m *alertModel) Init() tea.Cmd {
	return m.connectAlertSSE()
}

func (m *alertModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" || msg.String() == "esc" {
			return m, tea.Quit
		}

	case alertMsg:
		alert := models.PriceAlert(msg)
		m.alerts = append([]models.PriceAlert{alert}, m.alerts...)
		if len(m.alerts) > 50 {
			m.alerts = m.alerts[:50]
		}
		m.connected = true
		return m, m.connectAlertSSE()

	case errMsg:
		m.err = msg.err
		m.connected = false
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case tickMsg:
		return m, m.connectAlertSSE()
	}

	return m, nil
}

func (m *alertModel) View() string {
	var b strings.Builder

	b.WriteString(alertTitleStyle.Render(" PRICE ALERTS "))
	b.WriteString("\n\n")

	if m.err != nil && !m.connected {
		b.WriteString(redStyle.Render(fmt.Sprintf("  Connection error: %v\n\n", m.err)))
	}

	if len(m.alerts) == 0 {
		b.WriteString(dimStyle.Render("  Waiting for alerts...\n"))
		b.WriteString(dimStyle.Render("  Alerts trigger when prices cross configured thresholds.\n"))
	}

	for i, alert := range m.alerts {
		if i >= 15 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ... and %d more alerts\n", len(m.alerts)-15)))
			break
		}

		ts := time.Unix(alert.Timestamp, 0).Format("15:04:05")
		dirIcon := "▲"
		dirStyle := greenStyle
		if alert.Direction == "below" {
			dirIcon = "▼"
			dirStyle = redStyle
		}

		content := fmt.Sprintf(
			"%s %s %s  $%.2f  (threshold: $%.2f)  %s",
			dirStyle.Render(dirIcon),
			alertSymbolStyle.Render(alert.Symbol),
			alert.Direction,
			alert.Current,
			alert.Threshold,
			dimStyle.Render(ts),
		)

		b.WriteString(alertBoxStyle.Render(content))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.connected {
		b.WriteString(greenStyle.Render("  ● LISTENING"))
	} else {
		b.WriteString(redStyle.Render("  ● DISCONNECTED"))
	}
	b.WriteString(dimStyle.Render("  |  Press q to quit"))

	return b.String()
}

func (m *alertModel) connectAlertSSE() tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/stream/alerts", m.serverURL)
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
			var alert models.PriceAlert
			if err := json.Unmarshal([]byte(data), &alert); err != nil {
				continue
			}

			return alertMsg(alert)
		}

		return errMsg{fmt.Errorf("alert stream closed")}
	}
}
