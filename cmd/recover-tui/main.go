package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shubham/recovery/internal/carver"
	"github.com/shubham/recovery/internal/device"
	"github.com/shubham/recovery/internal/disk"
	"github.com/shubham/recovery/internal/fat32"
	"github.com/shubham/recovery/internal/ntfs"
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF00")).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7D56F4")).
			Bold(true)
)

// State represents the current screen
type State int

const (
	StateWelcome State = iota
	StateSelectSource
	StateSelectDevice
	StateEnterPath
	StateSelectMode
	StateSelectFileTypes
	StateSelectOutput
	StateConfirm
	StateRunning
	StateResults
)

// Source type
type SourceType int

const (
	SourceDevice SourceType = iota
	SourceImage
)

// Recovery mode
type RecoveryMode int

const (
	ModeScan RecoveryMode = iota
	ModeRecover
	ModeCarve
)

// File type filter
type FileTypeFilter struct {
	Name    string
	Enabled bool
}

// RecoveredFile for results
type RecoveredFileResult struct {
	Name string
	Path string
	Size int64
}

// Main model
type model struct {
	state        State
	width        int
	height       int
	err          error
	
	// Source selection
	sourceType   SourceType
	sourceList   list.Model
	
	// Device selection
	devices      []device.Device
	deviceList   list.Model
	selectedDevice *device.Device
	
	// Image path input
	pathInput    textinput.Model
	imagePath    string
	
	// Mode selection
	mode         RecoveryMode
	modeList     list.Model
	
	// File type selection
	fileTypes    []FileTypeFilter
	fileTypeCursor int
	
	// Output path
	outputInput  textinput.Model
	outputPath   string
	
	// Running state
	spinner      spinner.Model
	statusMsg    string
	progress     float64
	
	// Results
	results      []RecoveredFileResult
	resultCount  int
}

// List item for sources
type sourceItem struct {
	name string
	desc string
}
func (i sourceItem) Title() string       { return i.name }
func (i sourceItem) Description() string { return i.desc }
func (i sourceItem) FilterValue() string { return i.name }

// List item for devices
type deviceItem struct {
	device device.Device
}
func (i deviceItem) Title() string       { return fmt.Sprintf("%s - %s", i.device.Path, i.device.Name) }
func (i deviceItem) Description() string { 
	return fmt.Sprintf("%s | %s", i.device.SizeHuman, i.device.Filesystem)
}
func (i deviceItem) FilterValue() string { return i.device.Path }

// List item for modes
type modeItem struct {
	name string
	desc string
	mode RecoveryMode
}
func (i modeItem) Title() string       { return i.name }
func (i modeItem) Description() string { return i.desc }
func (i modeItem) FilterValue() string { return i.name }

// Messages
type devicesLoadedMsg struct {
	devices []device.Device
	err     error
}

type recoveryCompleteMsg struct {
	count int
	err   error
}

func initialModel() model {
	// Source list
	sourceItems := []list.Item{
		sourceItem{name: "📀 Physical Device", desc: "Recover from connected drive (USB, HDD, SSD)"},
		sourceItem{name: "📁 Disk Image", desc: "Recover from .img, .dd, or .raw file"},
	}
	sourceList := list.New(sourceItems, list.NewDefaultDelegate(), 0, 0)
	sourceList.Title = "Select Recovery Source"
	sourceList.SetShowStatusBar(false)
	sourceList.SetFilteringEnabled(false)

	// Mode list
	modeItems := []list.Item{
		modeItem{name: "🔍 Scan Only", desc: "List deleted files without recovering", mode: ModeScan},
		modeItem{name: "💾 Recover Files", desc: "Recover deleted files with original names", mode: ModeRecover},
		modeItem{name: "🔬 File Carving", desc: "Signature-based recovery (for damaged filesystems)", mode: ModeCarve},
	}
	modeList := list.New(modeItems, list.NewDefaultDelegate(), 0, 0)
	modeList.Title = "Select Recovery Mode"
	modeList.SetShowStatusBar(false)
	modeList.SetFilteringEnabled(false)

	// Path input
	pathInput := textinput.New()
	pathInput.Placeholder = "/path/to/disk.img"
	pathInput.Focus()
	pathInput.Width = 50

	// Output input
	outputInput := textinput.New()
	outputInput.Placeholder = "./recovered"
	outputInput.SetValue("./recovered")
	outputInput.Width = 50

	// Spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4"))

	// File types
	fileTypes := []FileTypeFilter{
		{Name: "Images (JPEG, PNG, GIF, BMP)", Enabled: true},
		{Name: "Videos (MP4, AVI, MKV, MOV)", Enabled: true},
		{Name: "Audio (MP3, WAV, FLAC)", Enabled: true},
		{Name: "Documents (PDF, DOCX, XLSX)", Enabled: true},
		{Name: "Archives (ZIP, RAR, 7Z)", Enabled: true},
		{Name: "All Other Types", Enabled: true},
	}

	return model{
		state:      StateWelcome,
		sourceList: sourceList,
		modeList:   modeList,
		pathInput:  pathInput,
		outputInput: outputInput,
		spinner:    s,
		fileTypes:  fileTypes,
		outputPath: "./recovered",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.state != StateRunning {
				return m, tea.Quit
			}
		case "esc":
			if m.state > StateWelcome && m.state != StateRunning {
				m.state--
				return m, nil
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sourceList.SetSize(msg.Width-4, msg.Height-10)
		m.modeList.SetSize(msg.Width-4, msg.Height-10)
		if m.deviceList.Items() != nil {
			m.deviceList.SetSize(msg.Width-4, msg.Height-10)
		}
		return m, nil

	case devicesLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.devices = msg.devices
		items := make([]list.Item, len(msg.devices))
		for i, d := range msg.devices {
			items[i] = deviceItem{device: d}
		}
		m.deviceList = list.New(items, list.NewDefaultDelegate(), m.width-4, m.height-10)
		m.deviceList.Title = "Select Device"
		m.deviceList.SetShowStatusBar(false)
		m.deviceList.SetFilteringEnabled(true)
		m.state = StateSelectDevice
		return m, nil

	case recoveryCompleteMsg:
		m.state = StateResults
		m.resultCount = msg.count
		if msg.err != nil {
			m.err = msg.err
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// State-specific updates
	switch m.state {
	case StateWelcome:
		return m.updateWelcome(msg)
	case StateSelectSource:
		return m.updateSelectSource(msg)
	case StateSelectDevice:
		return m.updateSelectDevice(msg)
	case StateEnterPath:
		return m.updateEnterPath(msg)
	case StateSelectMode:
		return m.updateSelectMode(msg)
	case StateSelectFileTypes:
		return m.updateSelectFileTypes(msg)
	case StateSelectOutput:
		return m.updateSelectOutput(msg)
	case StateConfirm:
		return m.updateConfirm(msg)
	case StateRunning:
		return m.updateRunning(msg)
	case StateResults:
		return m.updateResults(msg)
	}

	return m, nil
}

func (m model) updateWelcome(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if key.String() == "enter" {
			m.state = StateSelectSource
		}
	}
	return m, nil
}

func (m model) updateSelectSource(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		selected := m.sourceList.SelectedItem()
		if selected != nil {
			if strings.Contains(selected.(sourceItem).name, "Device") {
				m.sourceType = SourceDevice
				return m, m.loadDevices()
			} else {
				m.sourceType = SourceImage
				m.state = StateEnterPath
				m.pathInput.Focus()
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.sourceList, cmd = m.sourceList.Update(msg)
	return m, cmd
}

func (m model) updateSelectDevice(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		selected := m.deviceList.SelectedItem()
		if selected != nil {
			dev := selected.(deviceItem).device
			m.selectedDevice = &dev
			m.imagePath = dev.Path
			m.state = StateSelectMode
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.deviceList, cmd = m.deviceList.Update(msg)
	return m, cmd
}

func (m model) updateEnterPath(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		path := m.pathInput.Value()
		if path != "" {
			// Expand home directory
			if strings.HasPrefix(path, "~") {
				home, _ := os.UserHomeDir()
				path = filepath.Join(home, path[1:])
			}
			m.imagePath = path
			m.state = StateSelectMode
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.pathInput, cmd = m.pathInput.Update(msg)
	return m, cmd
}

func (m model) updateSelectMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		selected := m.modeList.SelectedItem()
		if selected != nil {
			m.mode = selected.(modeItem).mode
			if m.mode == ModeCarve {
				m.state = StateSelectFileTypes
			} else if m.mode == ModeScan {
				m.state = StateConfirm
			} else {
				m.state = StateSelectOutput
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.modeList, cmd = m.modeList.Update(msg)
	return m, cmd
}

func (m model) updateSelectFileTypes(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "k":
			if m.fileTypeCursor > 0 {
				m.fileTypeCursor--
			}
		case "down", "j":
			if m.fileTypeCursor < len(m.fileTypes)-1 {
				m.fileTypeCursor++
			}
		case " ":
			m.fileTypes[m.fileTypeCursor].Enabled = !m.fileTypes[m.fileTypeCursor].Enabled
		case "enter":
			m.state = StateSelectOutput
		}
	}
	return m, nil
}

func (m model) updateSelectOutput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		path := m.outputInput.Value()
		if path != "" {
			if strings.HasPrefix(path, "~") {
				home, _ := os.UserHomeDir()
				path = filepath.Join(home, path[1:])
			}
			m.outputPath = path
			m.state = StateConfirm
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.outputInput, cmd = m.outputInput.Update(msg)
	return m, cmd
}

func (m model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			m.state = StateRunning
			m.statusMsg = "Starting recovery..."
			return m, tea.Batch(m.spinner.Tick, m.runRecovery())
		case "n", "N":
			m.state = StateSelectSource
		}
	}
	return m, nil
}

func (m model) updateRunning(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m model) updateResults(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter", "q":
			return m, tea.Quit
		case "r":
			// Restart
			return initialModel(), nil
		}
	}
	return m, nil
}

func (m model) loadDevices() tea.Cmd {
	return func() tea.Msg {
		devices, err := device.List()
		return devicesLoadedMsg{devices: devices, err: err}
	}
}

func (m model) runRecovery() tea.Cmd {
	return func() tea.Msg {
		reader, err := disk.Open(m.imagePath)
		if err != nil {
			return recoveryCompleteMsg{err: err}
		}
		defer reader.Close()

		var count int

		if m.mode == ModeCarve {
			count, err = carver.Recover(reader, m.outputPath, m.mode == ModeScan)
		} else {
			fsType, detectErr := disk.DetectFilesystem(reader)
			if detectErr != nil {
				return recoveryCompleteMsg{err: detectErr}
			}

			switch fsType {
			case "ntfs":
				count, err = ntfs.Recover(reader, m.outputPath, m.mode == ModeScan, false)
			case "fat32":
				count, err = fat32.Recover(reader, m.outputPath, m.mode == ModeScan, false)
			default:
				return recoveryCompleteMsg{err: fmt.Errorf("unsupported filesystem: %s", fsType)}
			}
		}

		return recoveryCompleteMsg{count: count, err: err}
	}
}

func (m model) View() string {
	var s strings.Builder

	// Header
	s.WriteString(titleStyle.Render(" 🔧 Data Recovery Tool "))
	s.WriteString("\n\n")

	switch m.state {
	case StateWelcome:
		s.WriteString(m.viewWelcome())
	case StateSelectSource:
		s.WriteString(m.sourceList.View())
	case StateSelectDevice:
		s.WriteString(m.deviceList.View())
	case StateEnterPath:
		s.WriteString(m.viewEnterPath())
	case StateSelectMode:
		s.WriteString(m.modeList.View())
	case StateSelectFileTypes:
		s.WriteString(m.viewSelectFileTypes())
	case StateSelectOutput:
		s.WriteString(m.viewSelectOutput())
	case StateConfirm:
		s.WriteString(m.viewConfirm())
	case StateRunning:
		s.WriteString(m.viewRunning())
	case StateResults:
		s.WriteString(m.viewResults())
	}

	// Error display
	if m.err != nil {
		s.WriteString("\n\n")
		s.WriteString(errorStyle.Render("Error: " + m.err.Error()))
	}

	// Footer
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press q to quit • esc to go back"))

	return s.String()
}

func (m model) viewWelcome() string {
	var s strings.Builder
	s.WriteString(subtitleStyle.Render("Welcome to Data Recovery Tool"))
	s.WriteString("\n\n")
	s.WriteString("This tool helps you recover deleted files from:\n")
	s.WriteString("  • FAT32 drives (USB drives, SD cards)\n")
	s.WriteString("  • NTFS drives (Windows hard drives)\n")
	s.WriteString("  • Disk images (.img, .dd, .raw files)\n\n")
	s.WriteString("⚠️  ")
	s.WriteString(lipgloss.NewStyle().Bold(true).Render("Important:"))
	s.WriteString(" This tool is READ-ONLY and will not modify your drive.\n")
	s.WriteString("   For best results, create a disk image first.\n\n")
	s.WriteString(selectedStyle.Render("Press Enter to continue..."))
	return s.String()
}

func (m model) viewEnterPath() string {
	var s strings.Builder
	s.WriteString(subtitleStyle.Render("Enter Disk Image Path"))
	s.WriteString("\n\n")
	s.WriteString("Enter the path to your disk image file:\n\n")
	s.WriteString(m.pathInput.View())
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Enter to continue"))
	return s.String()
}

func (m model) viewSelectFileTypes() string {
	var s strings.Builder
	s.WriteString(subtitleStyle.Render("Select File Types to Recover"))
	s.WriteString("\n\n")

	for i, ft := range m.fileTypes {
		cursor := "  "
		if i == m.fileTypeCursor {
			cursor = "> "
		}

		checkbox := "[ ]"
		if ft.Enabled {
			checkbox = "[✓]"
		}

		line := fmt.Sprintf("%s%s %s", cursor, checkbox, ft.Name)
		if i == m.fileTypeCursor {
			s.WriteString(selectedStyle.Render(line))
		} else {
			s.WriteString(line)
		}
		s.WriteString("\n")
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/↓ to move • Space to toggle • Enter to continue"))
	return s.String()
}

func (m model) viewSelectOutput() string {
	var s strings.Builder
	s.WriteString(subtitleStyle.Render("Select Output Directory"))
	s.WriteString("\n\n")
	s.WriteString("Where should recovered files be saved?\n\n")
	s.WriteString(m.outputInput.View())
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Enter to continue"))
	return s.String()
}

func (m model) viewConfirm() string {
	var s strings.Builder
	s.WriteString(subtitleStyle.Render("Confirm Recovery Settings"))
	s.WriteString("\n\n")

	s.WriteString(fmt.Sprintf("  Source:  %s\n", m.imagePath))

	modeStr := "Scan Only"
	if m.mode == ModeRecover {
		modeStr = "Recover Files"
	} else if m.mode == ModeCarve {
		modeStr = "File Carving"
	}
	s.WriteString(fmt.Sprintf("  Mode:    %s\n", modeStr))

	if m.mode != ModeScan {
		s.WriteString(fmt.Sprintf("  Output:  %s\n", m.outputPath))
	}

	s.WriteString("\n")
	s.WriteString("⚠️  The source will be opened in READ-ONLY mode.\n\n")
	s.WriteString(selectedStyle.Render("Press Y to start, N to go back"))
	return s.String()
}

func (m model) viewRunning() string {
	var s strings.Builder
	s.WriteString(m.spinner.View())
	s.WriteString(" ")
	s.WriteString(m.statusMsg)
	s.WriteString("\n\n")
	s.WriteString("This may take a while for large drives...\n")
	s.WriteString(helpStyle.Render("Please wait..."))
	return s.String()
}

func (m model) viewResults() string {
	var s strings.Builder

	if m.err != nil {
		s.WriteString(errorStyle.Render("Recovery Failed"))
		s.WriteString("\n\n")
		s.WriteString(fmt.Sprintf("Error: %v\n", m.err))
	} else {
		s.WriteString(successStyle.Render("✓ Recovery Complete!"))
		s.WriteString("\n\n")
		s.WriteString(fmt.Sprintf("Found %d deleted files.\n", m.resultCount))
		if m.mode != ModeScan {
			s.WriteString(fmt.Sprintf("Files saved to: %s\n", m.outputPath))
		}
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("Press R to run again • Q to quit"))
	return s.String()
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
