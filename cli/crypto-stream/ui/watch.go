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
	watchTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#FF6600")).
			Padding(0, 2)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Width(16)

	valueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF"))

	bigPriceStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Padding(0, 1)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).
			Padding(1, 2).
			Width(50)

	sparkUp   = greenStyle.Render("▲")
	sparkDown = redStyle.Render("▼")
)

type watchModel struct {
	symbol    string
	price     *models.AggregatedPrice
	history   []float64
	serverURL string
	connected bool
	err       error
}

func RunWatchMode(serverURL string, symbol string) {
	m := &watchModel{
		symbol:    symbol,
		serverURL: serverURL,
		history:   make([]float64, 0, 60),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func (m *watchModel) Init() tea.Cmd {
	return m.connectSSE()
}

func (m *watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" || msg.String() == "esc" {
			return m, tea.Quit
		}

	case priceMsg:
		p := models.AggregatedPrice(msg)
		m.price = &p
		m.connected = true
		m.history = append(m.history, p.Price)
		if len(m.history) > 60 {
			m.history = m.history[1:]
		}
		return m, m.connectSSE()

	case errMsg:
		m.err = msg.err
		m.connected = false
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case tickMsg:
		return m, m.connectSSE()
	}

	return m, nil
}

func (m *watchModel) View() string {
	var b strings.Builder

	b.WriteString(watchTitleStyle.Render(fmt.Sprintf(" WATCHING %s ", m.symbol)))
	b.WriteString("\n\n")

	if m.price == nil {
		if m.err != nil {
			b.WriteString(redStyle.Render(fmt.Sprintf("  Connection error: %v\n", m.err)))
		} else {
			b.WriteString(dimStyle.Render("  Waiting for data...\n"))
		}
		b.WriteString(dimStyle.Render("\n  Press q to quit"))
		return b.String()
	}

	p := m.price

	// Direction indicator
	dir := sparkUp
	if p.ChangePct < 0 {
		dir = sparkDown
	}

	// Price display
	priceStr := fmt.Sprintf("$%.2f %s", p.Price, dir)
	if p.Price < 1 {
		priceStr = fmt.Sprintf("$%.6f %s", p.Price, dir)
	}
	b.WriteString(bigPriceStyle.Render(priceStr))
	b.WriteString("\n\n")

	// Details box
	var details strings.Builder
	details.WriteString(row("Average Price", formatPrice(p.AvgPrice)))
	details.WriteString(row("24h High", formatPrice(p.HighPrice)))
	details.WriteString(row("24h Low", formatPrice(p.LowPrice)))

	changeStr := fmt.Sprintf("%.2f%%", p.ChangePct)
	if p.ChangePct > 0 {
		changeStr = greenStyle.Render("+" + changeStr)
	} else {
		changeStr = redStyle.Render(changeStr)
	}
	details.WriteString(row("Change", changeStr))
	details.WriteString(row("Volume", formatVolume(p.Volume)))
	details.WriteString(row("Exchanges", fmt.Sprintf("%d", p.ExchangeCount)))
	details.WriteString(row("Updated", time.Unix(p.Timestamp, 0).Format("15:04:05")))

	b.WriteString(boxStyle.Render(details.String()))
	b.WriteString("\n\n")

	// Mini sparkline
	if len(m.history) > 1 {
		b.WriteString("  Price History (last 60 updates):\n  ")
		b.WriteString(renderSparkline(m.history))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.connected {
		b.WriteString(greenStyle.Render("  ● LIVE"))
	} else {
		b.WriteString(redStyle.Render("  ● DISCONNECTED"))
	}
	b.WriteString(dimStyle.Render("  |  Press q to quit"))

	return b.String()
}

func (m *watchModel) connectSSE() tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/stream?symbols=%s", m.serverURL, m.symbol)
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

		return errMsg{fmt.Errorf("stream closed")}
	}
}

func row(label, value string) string {
	return labelStyle.Render(label+":") + " " + valueStyle.Render(value) + "\n"
}

func renderSparkline(data []float64) string {
	if len(data) == 0 {
		return ""
	}

	blocks := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

	min, max := data[0], data[0]
	for _, v := range data {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	rng := max - min
	if rng == 0 {
		rng = 1
	}

	var sb strings.Builder
	for i, v := range data {
		idx := int((v - min) / rng * float64(len(blocks)-1))
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}

		char := blocks[idx]
		if i > 0 && v > data[i-1] {
			sb.WriteString(greenStyle.Render(char))
		} else if i > 0 && v < data[i-1] {
			sb.WriteString(redStyle.Render(char))
		} else {
			sb.WriteString(char)
		}
	}

	return sb.String()
}
