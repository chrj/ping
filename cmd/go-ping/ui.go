package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chrj/ping"
)

var blocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	faintStyle  = lipgloss.NewStyle().Faint(true)
	chartStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	doneStyle   = lipgloss.NewStyle().Faint(true).Italic(true)
)

type replyMsg ping.Reply
type channelClosedMsg struct{}

type model struct {
	replies  <-chan ping.Reply
	cancel   context.CancelFunc
	target   string
	interval time.Duration

	width, height int
	chartCap      int

	samples  []time.Duration
	sent     int
	lost     int
	minRTT   time.Duration
	maxRTT   time.Duration
	totalRTT time.Duration

	done bool
}

func newModel(replies <-chan ping.Reply, cancel context.CancelFunc, target string, interval time.Duration) model {
	return model{
		replies:  replies,
		cancel:   cancel,
		target:   target,
		interval: interval,
		chartCap: 80,
		minRTT:   math.MaxInt64,
	}
}

func waitForReply(ch <-chan ping.Reply) tea.Cmd {
	return func() tea.Msg {
		reply, ok := <-ch
		if !ok {
			return channelClosedMsg{}
		}
		return replyMsg(reply)
	}
}

func (m model) Init() tea.Cmd {
	return waitForReply(m.replies)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cap := m.width - 12
		if cap < 10 {
			cap = 10
		}
		m.chartCap = cap
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit
		}

	case replyMsg:
		m.sent++
		r := ping.Reply(msg)
		if r.Err != nil {
			m.samples = append(m.samples, -1)
			m.lost++
		} else {
			m.samples = append(m.samples, r.RTT)
			m.totalRTT += r.RTT
			if r.RTT < m.minRTT {
				m.minRTT = r.RTT
			}
			if r.RTT > m.maxRTT {
				m.maxRTT = r.RTT
			}
		}
		return m, waitForReply(m.replies)

	case channelClosedMsg:
		m.done = true
		return m, nil
	}

	return m, nil
}

func (m model) View() string {
	left := headerStyle.Render(fmt.Sprintf("go-ping  target: %s  interval: %s", m.target, m.interval))
	right := faintStyle.Render("q to quit")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	header := left + strings.Repeat(" ", gap) + right

	chart := chartStyle.Render(barChart(m.samples, m.chartCap, 8, m.interval))

	var avgStr string
	received := m.sent - m.lost
	if received > 0 {
		avgStr = fmtDur(m.totalRTT / time.Duration(received))
	} else {
		avgStr = "-"
	}

	var minStr string
	if m.minRTT == math.MaxInt64 {
		minStr = "-"
	} else {
		minStr = fmtDur(m.minRTT)
	}

	stats := fmt.Sprintf("  sent: %d   lost: %d   min: %s   avg: %s   max: %s",
		m.sent, m.lost, minStr, avgStr, fmtDur(m.maxRTT))

	lines := []string{header, "", chart, "", stats}
	if m.done {
		lines = append(lines, "", doneStyle.Render("  --- ping finished ---"))
	}
	return strings.Join(lines, "\n")
}

func barChart(samples []time.Duration, width, height int, interval time.Duration) string {
	visible := samples
	if len(samples) > width {
		visible = samples[len(samples)-width:]
	}

	var maxRTT time.Duration
	for _, s := range visible {
		if s > maxRTT {
			maxRTT = s
		}
	}

	const numYLabels = 4
	yLabelAt := make(map[int]string, numYLabels)
	for i := 0; i < numYLabels; i++ {
		row := i * (height - 1) / (numYLabels - 1)
		val := maxRTT * time.Duration(height-1-row) / time.Duration(height-1)
		if row == height-1 {
			yLabelAt[row] = "0ms"
		} else {
			yLabelAt[row] = fmtDur(val)
		}
	}
	labelW := 3 // minimum for "0ms"
	for _, lbl := range yLabelAt {
		if len(lbl) > labelW {
			labelW = len(lbl)
		}
	}

	totalUnits := height * len(blocks)
	rows := make([]strings.Builder, height)

	for _, s := range visible {
		if s < 0 {
			for r := 0; r < height-1; r++ {
				rows[r].WriteRune(' ')
			}
			rows[height-1].WriteRune('?')
			continue
		}

		var filledUnits int
		if maxRTT > 0 {
			filledUnits = int(float64(s) / float64(maxRTT) * float64(totalUnits))
			if filledUnits > totalUnits {
				filledUnits = totalUnits
			}
		}

		fullRows := filledUnits / len(blocks)
		partialIdx := filledUnits % len(blocks)

		for r := 0; r < height; r++ {
			rowFromBottom := height - 1 - r
			switch {
			case rowFromBottom < fullRows:
				rows[r].WriteRune('█')
			case rowFromBottom == fullRows && partialIdx > 0:
				rows[r].WriteRune(blocks[partialIdx-1])
			default:
				rows[r].WriteRune(' ')
			}
		}
	}

	chartW := len(visible)
	lines := make([]string, 0, height+2)
	for r := 0; r < height; r++ {
		var yLabel, axisChar string
		if lbl, ok := yLabelAt[r]; ok {
			yLabel = fmt.Sprintf("%*s", labelW, lbl)
			axisChar = "┤"
		} else {
			yLabel = strings.Repeat(" ", labelW)
			axisChar = "│"
		}
		lines = append(lines, yLabel+axisChar+rows[r].String())
	}

	lines = append(lines, strings.Repeat(" ", labelW)+"└"+strings.Repeat("─", chartW))
	lines = append(lines, xAxisLabels(chartW, labelW+1, interval))

	return strings.Join(lines, "\n")
}

func xAxisLabels(n, offset int, interval time.Duration) string {
	if n == 0 {
		return ""
	}

	buf := make([]rune, offset+n)
	for i := range buf {
		buf[i] = ' '
	}

	place := func(col int, label string) {
		runes := []rune(label)
		pos := offset + col - len(runes)/2
		for i, ch := range runes {
			if p := pos + i; p >= 0 && p < len(buf) {
				buf[p] = ch
			}
		}
	}

	// Right-align "now" so it doesn't overflow the buffer.
	nowRunes := []rune("now")
	for i, ch := range nowRunes {
		if p := offset + n - len(nowRunes) + i; p >= 0 && p < len(buf) {
			buf[p] = ch
		}
	}

	step := 20
	for col := n - 1 - step; col >= 0; col -= step {
		d := time.Duration(n-1-col) * interval
		var label string
		if d < time.Minute {
			label = fmt.Sprintf("-%ds", int(d.Seconds()))
		} else {
			label = fmt.Sprintf("-%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
		}
		place(col, label)
	}

	return string(buf)
}

func fmtDur(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	ms := float64(d) / float64(time.Millisecond)
	return fmt.Sprintf("%.2fms", ms)
}
