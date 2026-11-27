package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/fleet"
	"github.com/flobilosaurus/agentnook/internal/images"
)

type WatchTab uint8

const (
	WatchVMs WatchTab = iota
	WatchApprovals
	WatchAudit
	WatchImages
)

var watchTabs = [...]string{"VMs", "Approvals", "Audit", "Images"}

const (
	watchChromeRows = 5
	watchMinWidth   = 48
	watchMinHeight  = 14 + watchChromeRows
)

type WatchOptions struct {
	Root       string
	Start      WatchTab
	History    egress.HistoryQuery
	LoadFleet  FleetLoader
	LoadImages func() (images.CatalogStatus, error)
}

// Watch owns the terminal and its pollers. Closing it does not stop VMs or brokers.
func Watch(options WatchOptions, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := tea.NewProgram(newWatchModel(ctx, options), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	return err
}

type watchImagesLoaded struct {
	status images.CatalogStatus
	err    error
}

type watchModel struct {
	root          string
	tab           WatchTab
	width, height int
	fleet         fleetModel
	approvals     networkModel
	audit         networkModel
	images        configStatusModel
	loadImages    func() (images.CatalogStatus, error)
	imageLoading  bool
	imageReady    bool
	imageErr      error
	sized         bool
}

func newWatchModel(ctx context.Context, options WatchOptions) watchModel {
	readHistory := func(query egress.TimelineQuery) (egress.Timeline, error) {
		return egress.ReadTimeline(ctx, options.Root, query)
	}
	approvals := newNetworkModel(false, egress.HistoryQuery{}, egress.Decide)
	approvals.loadHistory = readHistory
	approvals.loadLive = func() (egress.Survey, error) { return egress.Load(options.Root) }
	audit := newNetworkModel(true, options.History, nil)
	audit.loadHistory = readHistory
	audit.checkRuntime = func() tea.Msg {
		running, err := fleet.RunningMachineNames(ctx)
		return networkRuntimeLoaded{running: running, err: err}
	}
	m := watchModel{root: options.Root, tab: options.Start, fleet: newFleetModel(ctx, options.LoadFleet), approvals: approvals, audit: audit,
		loadImages: options.LoadImages, imageLoading: options.Start == WatchImages}
	m.resize(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func (m watchModel) fetchImages() tea.Msg {
	if m.loadImages == nil {
		return watchImagesLoaded{}
	}
	status, err := m.loadImages()
	return watchImagesLoaded{status, err}
}

func (m watchModel) Init() tea.Cmd {
	var imageCmd, fleetCmd tea.Cmd
	if m.imageLoading {
		imageCmd = m.fetchImages
	}
	if m.fleet.load != nil {
		fleetCmd = m.fleet.Init()
	}
	return tea.Batch(fleetCmd, m.approvals.Init(), m.audit.Init(), imageCmd)
}

func (m *watchModel) resize(size tea.WindowSizeMsg) {
	m.width, m.height = max(1, size.Width), max(1, size.Height)
	body := tea.WindowSizeMsg{Width: m.width, Height: max(1, m.height-watchChromeRows)}
	fleetPane, _ := m.fleet.Update(body)
	m.fleet = fleetPane.(fleetModel)
	m.approvals.width, m.approvals.height = body.Width, body.Height
	m.audit.width, m.audit.height = body.Width, body.Height
	imagesPane, _ := m.images.Update(body)
	m.images = imagesPane.(configStatusModel)
}

func (m *watchModel) switchTab(tab WatchTab) tea.Cmd {
	if m.tab == WatchApprovals && tab != WatchApprovals && m.approvals.selected.request != "" {
		m.approvals.selected = networkKeyID{}
	}
	m.tab = tab
	if tab == WatchImages && !m.imageLoading && !m.imageReady {
		m.imageLoading = true
		return m.fetchImages
	}
	return nil
}

func (m *watchModel) scopeAudit(machine string) tea.Cmd {
	m.audit.machine, m.audit.runningOnly = machine, false
	m.audit.position, m.audit.newer = timelinePosition{}, nil
	return m.audit.reloadHistory()
}

func (m *watchModel) updateFleet(msg tea.Msg) tea.Cmd {
	next, cmd := m.fleet.Update(msg)
	m.fleet = next.(fleetModel)
	return cmd
}

func updateNetwork(pane *networkModel, msg tea.Msg) tea.Cmd {
	next, cmd := pane.Update(msg)
	*pane = next.(networkModel)
	return cmd
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.sized = true
		m.resize(msg)
	case fleetRefreshMsg, fleetResultMsg:
		cmd = m.updateFleet(msg)
	case networkLiveTick, networkLiveLoaded, networkDecided:
		cmd = updateNetwork(&m.approvals, msg)
	case networkRuntimeTick:
		cmd = updateNetwork(&m.audit, msg)
	case networkRuntimeLoaded:
		cmd = updateNetwork(&m.audit, msg)
		m.approvals.running = m.audit.running
	case networkHistoryLoaded:
		if msg.archive {
			cmd = updateNetwork(&m.audit, msg)
		} else {
			cmd = updateNetwork(&m.approvals, msg)
		}
	case networkHistoryTick:
		if msg.archive {
			cmd = updateNetwork(&m.audit, msg)
		} else {
			cmd = updateNetwork(&m.approvals, msg)
		}
	case watchImagesLoaded:
		m.imageLoading, m.imageReady, m.imageErr = false, true, msg.err
		if msg.err == nil {
			m.images = newConfigStatusModel(msg.status)
			m.resize(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		}
	case tea.KeyMsg:
		cmd = m.key(msg)
	}
	return m, cmd
}

func (m *watchModel) key(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "q", "esc", "ctrl+c":
		return tea.Quit
	case "tab", "]":
		return m.switchTab(WatchTab((int(m.tab) + 1) % len(watchTabs)))
	case "shift+tab", "[":
		return m.switchTab(WatchTab((int(m.tab) + len(watchTabs) - 1) % len(watchTabs)))
	case "1", "2", "3", "4":
		return m.switchTab(WatchTab(key[0] - '1'))
	}
	if m.width < watchMinWidth || m.height < watchMinHeight {
		return nil
	}
	switch m.tab {
	case WatchVMs:
		if key == "enter" && m.fleet.instance() != nil {
			m.tab = WatchAudit
			return m.scopeAudit(m.fleet.instance().Name)
		}
		return m.updateFleet(msg)
	case WatchApprovals:
		if !m.sized {
			return nil
		}
		return updateNetwork(&m.approvals, msg)
	case WatchAudit:
		if key == "x" && m.audit.machine != "" {
			return m.scopeAudit("")
		}
		return updateNetwork(&m.audit, msg)
	case WatchImages:
		if key == "r" && !m.imageLoading {
			m.imageLoading = true
			return m.fetchImages
		}
		if !m.imageReady || m.imageErr != nil {
			return nil
		}
		pane, cmd := m.images.Update(msg)
		m.images = pane.(configStatusModel)
		return cmd
	}
	return nil
}

func (m watchModel) tabBar() string {
	return renderWatchTabs(m.width, m.tab, len(m.approvals.approvalLeaves(time.Now())))
}

func (m watchModel) body() string {
	switch m.tab {
	case WatchApprovals:
		return m.approvals.View()
	case WatchAudit:
		return m.audit.View()
	case WatchImages:
		if m.imageLoading || !m.imageReady {
			return "Loading image status…"
		}
		if m.imageErr != nil {
			return monitorWarning.Render("Image status unavailable: "+monitorText(m.imageErr.Error())) + "\nr retry"
		}
		return m.images.View()
	default:
		return m.fleet.View()
	}
}

func (m watchModel) View() string {
	if m.width < watchMinWidth || m.height < watchMinHeight {
		message := fmt.Sprintf("Watch needs at least %d×%d. Resize or q to quit.", watchMinWidth, watchMinHeight)
		return monitorView([]string{message}, m.width)
	}
	lines := []string{monitorTitle.Render("nook watch"), monitorMuted.Render("All projects · AGENTNOOK_HOME: " + monitorText(m.root))}
	lines = append(lines, strings.Split(m.tabBar(), "\n")...)
	body := strings.Split(m.body(), "\n")
	lines = append(lines, body[:min(len(body), m.height-watchChromeRows)]...)
	help := "1–4 · Tab/[ ] switch tabs · q quit"
	if m.tab == WatchImages {
		help += " · r refresh"
	}
	return monitorView(append(lines, monitorMuted.Render(help)), m.width)
}
