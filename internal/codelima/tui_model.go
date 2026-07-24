package codelima

import (
	"context"
	"errors"
	"fmt"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type TUIRunner interface {
	Run(ctx context.Context, service *Service, workspaceRoot string) error
}

type tuiFocus string

const (
	tuiFocusTree     tuiFocus = "tree"
	tuiFocusTerminal tuiFocus = "terminal"
)

type tuiTreePaneMode string

const (
	tuiTreePaneModeTerminal tuiTreePaneMode = "terminal"
	tuiTreePaneModeInfo     tuiTreePaneMode = "info"
)

type tuiSessionManager interface {
	HasSession(sessionKey string) bool
	TargetSessionKeys(targetKey string) []string
	MoveTab(targetKey, sessionKey string, direction int) error
	OpenNodeTab(node Node) (string, error)
	OpenNodeHostTab(node Node) (string, error)
}

type tuiActionID string

const (
	tuiActionConfigurationManage     tuiActionID = "configuration.manage"
	tuiActionNodeCreate              tuiActionID = "node.create"
	tuiActionEnvironmentConfigManage tuiActionID = "environment_config.manage"
	tuiActionNodeStart               tuiActionID = "node.start"
	tuiActionNodeStop                tuiActionID = "node.stop"
	tuiActionNodeDelete              tuiActionID = "node.delete"
	tuiActionNodeClone               tuiActionID = "node.clone"
)

type tuiActionSpec struct {
	ID     tuiActionID
	Label  string
	Hotkey rune
}

type tuiNoopSessionManager struct{}

func (tuiNoopSessionManager) HasSession(string) bool {
	return false
}

func (tuiNoopSessionManager) TargetSessionKeys(string) []string {
	return nil
}

func (tuiNoopSessionManager) MoveTab(string, string, int) error {
	return nil
}

func (tuiNoopSessionManager) OpenNodeTab(Node) (string, error) {
	return "", nil
}

func (tuiNoopSessionManager) OpenNodeHostTab(Node) (string, error) {
	return "", nil
}

// tuiTreeEntry is one selectable item in the flat node list. Rendering may use
// multiple terminal rows for the item. A zero entry (empty node ID) is the
// "nothing selected" sentinel.
type tuiTreeEntry struct {
	node Node
}

func (e tuiTreeEntry) valid() bool {
	return e.node.ID != ""
}

func (e tuiTreeEntry) key() string {
	if !e.valid() {
		return ""
	}
	return terminal.NodeTarget(e.node.ID).String()
}

type tuiState struct {
	nodes          []Node
	entries        []tuiTreeEntry
	nodesByID      map[string]Node
	selection      int
	scroll         int
	focus          tuiFocus
	treePaneMode   tuiTreePaneMode
	terminalTarget string
	activeTabKeys  map[terminal.TargetKey]terminal.TabID
	sessions       tuiSessionManager
}

func newTUIState(nodes []Node, sessions tuiSessionManager) (*tuiState, error) {
	if sessions == nil {
		sessions = tuiNoopSessionManager{}
	}

	state := &tuiState{
		nodes:         append([]Node(nil), nodes...),
		nodesByID:     map[string]Node{},
		selection:     -1,
		focus:         tuiFocusTree,
		treePaneMode:  tuiTreePaneModeInfo,
		activeTabKeys: map[terminal.TargetKey]terminal.TabID{},
		sessions:      sessions,
	}

	state.indexNodes()
	state.rebuildEntries()
	if len(state.entries) == 0 {
		return state, nil
	}

	if err := state.selectIndex(0); err != nil {
		return nil, err
	}
	state.treePaneMode = defaultTUITreePaneMode(state.selectedEntry())

	return state, nil
}

func defaultTUITreePaneMode(entry tuiTreeEntry) tuiTreePaneMode {
	if entry.valid() && nodeIsRunning(entry.node) {
		return tuiTreePaneModeTerminal
	}
	return tuiTreePaneModeInfo
}

func (s *tuiState) indexNodes() {
	s.nodesByID = map[string]Node{}
	for _, node := range s.nodes {
		s.nodesByID[node.ID] = node
	}
}

func (s *tuiState) rebuildEntries() {
	selectedKey := s.selectedEntry().key()
	entries := make([]tuiTreeEntry, 0, len(s.nodes))
	for _, node := range s.nodes {
		entries = append(entries, tuiTreeEntry{node: node})
	}
	s.entries = entries

	if len(s.entries) == 0 {
		s.selection = -1
		s.scroll = 0
		return
	}

	switch {
	case selectedKey != "":
		if index := s.findEntryByKey(selectedKey); index >= 0 {
			s.selection = index
			return
		}
	case s.terminalTarget != "":
		if index := s.findEntryByKey(s.terminalTarget); index >= 0 {
			s.selection = index
			return
		}
	}

	if s.selection < 0 || s.selection >= len(s.entries) {
		s.selection = 0
	}
}

func (s *tuiState) selectedEntry() tuiTreeEntry {
	if s.selection < 0 || s.selection >= len(s.entries) {
		return tuiTreeEntry{}
	}

	return s.entries[s.selection]
}

func (s *tuiState) findEntryByKey(key string) int {
	for index, entry := range s.entries {
		if entry.key() == key {
			return index
		}
	}

	return -1
}

func (s *tuiState) selectIndex(index int) error {
	if len(s.entries) == 0 {
		s.selection = -1
		s.treePaneMode = tuiTreePaneModeInfo
		return nil
	}

	previous := s.selectedEntry()
	if index < 0 {
		index = 0
	}
	if index >= len(s.entries) {
		index = len(s.entries) - 1
	}

	s.selection = index
	s.applySelectedNodeDefault(previous)
	return nil
}

// applySelectedNodeDefault chooses the useful pane whenever selection changes
// or the selected VM crosses the running boundary. An explicit i toggle remains
// stable while the same node retains the same runtime readiness.
func (s *tuiState) applySelectedNodeDefault(previous tuiTreeEntry) {
	current := s.selectedEntry()
	if current.key() == previous.key() &&
		nodeIsRunning(current.node) == nodeIsRunning(previous.node) {
		return
	}
	s.treePaneMode = defaultTUITreePaneMode(current)
}

func (s *tuiState) moveSelection(delta int) error {
	if len(s.entries) == 0 || delta == 0 {
		return nil
	}

	return s.selectIndex(s.selection + delta)
}

func (s *tuiState) focusTerminal() error {
	return s.focusTerminalEntry(s.selectedEntry())
}

func (s *tuiState) focusTerminalEntry(entry tuiTreeEntry) error {
	if !entry.valid() {
		return errors.New("select a node to focus the terminal")
	}
	if !nodeIsRunning(entry.node) {
		return errors.New("selected node is not running; start it before focusing the terminal")
	}

	sessionKey, err := s.ensureTargetTab(entry)
	if err != nil {
		return err
	}
	if sessionKey == "" || !s.sessions.HasSession(sessionKey) {
		return errors.New("no terminal session is active")
	}

	s.terminalTarget = entry.key()
	s.focus = tuiFocusTerminal
	return nil
}

// ensureTargetTab reuses the entry's active terminal tab, opening the first
// tab for the entry when none is open yet.
func (s *tuiState) ensureTargetTab(entry tuiTreeEntry) (string, error) {
	targetKey := entry.key()
	if targetKey == "" {
		return "", errors.New("select a node to focus the terminal")
	}
	if sessionKey := s.targetActiveSessionKey(targetKey); sessionKey != "" {
		s.setActiveTab(targetKey, sessionKey)
		return sessionKey, nil
	}
	return s.openTerminalTabEntry(entry)
}

// ensureSelectedTerminalTab makes one terminal tab available for the selected
// running node without changing tree focus or opening a shell for a stopped
// node.
func (s *tuiState) ensureSelectedTerminalTab() error {
	entry := s.selectedEntry()
	if !entry.valid() {
		return nil
	}
	if s.targetActiveSessionKey(entry.key()) != "" {
		return nil
	}
	if !nodeIsRunning(entry.node) {
		return nil
	}
	_, err := s.openTerminalTabEntry(entry)
	return err
}

// openTerminalTabEntry opens a fresh terminal tab for the entry and makes it
// the entry's active tab.
func (s *tuiState) openTerminalTabEntry(entry tuiTreeEntry) (string, error) {
	if !entry.valid() {
		return "", errors.New("select a node to open a terminal tab")
	}
	if !nodeIsRunning(entry.node) {
		return "", errors.New("selected node is not running; start it before opening a terminal tab")
	}
	sessionKey, err := s.sessions.OpenNodeTab(entry.node)
	if err != nil {
		return "", fmt.Errorf("start shell for %s: %w", entry.node.Slug, err)
	}
	s.setActiveTab(entry.key(), sessionKey)
	return sessionKey, nil
}

// openHostTerminalTabEntry opens a fresh host shell tab for a node and makes
// it active. Host shells are ordinary node-scoped tabs: they participate in
// the same switching, closing, and refresh behavior as guest shell tabs.
func (s *tuiState) openHostTerminalTabEntry(entry tuiTreeEntry) (string, error) {
	if !entry.valid() {
		return "", errors.New("select a node to open a host terminal tab")
	}
	sessionKey, err := s.sessions.OpenNodeHostTab(entry.node)
	if err != nil {
		return "", fmt.Errorf("start host shell for %s: %w", entry.node.Slug, err)
	}
	if sessionKey == "" || !s.sessions.HasSession(sessionKey) {
		return "", errors.New("no host terminal session is active")
	}
	s.setActiveTab(entry.key(), sessionKey)
	return sessionKey, nil
}

func (s *tuiState) setActiveTab(targetKey, sessionKey string) {
	if targetKey == "" || sessionKey == "" {
		return
	}
	tk, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return
	}
	s.activeTabKeys[tk] = terminal.TabID(sessionKey)
}

// activeTab returns the session key of the target's recorded active tab, or ""
// when none is recorded. It is the single reader of the activeTabKeys map so
// the target-key parsing stays in one place.
func (s *tuiState) activeTab(targetKey string) string {
	tk, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return ""
	}
	return string(s.activeTabKeys[tk])
}

// clearActiveTab forgets the target's recorded active tab.
func (s *tuiState) clearActiveTab(targetKey string) {
	tk, err := terminal.ParseTargetKey(targetKey)
	if err != nil {
		return
	}
	delete(s.activeTabKeys, tk)
}

// targetActiveSessionKey resolves the active tab for a target, falling back
// to the target's first open tab when the recorded tab is gone.
func (s *tuiState) targetActiveSessionKey(targetKey string) string {
	if targetKey == "" {
		return ""
	}
	keys := s.sessions.TargetSessionKeys(targetKey)
	if len(keys) == 0 {
		return ""
	}
	if active := s.activeTab(targetKey); active != "" && containsString(keys, active) {
		return active
	}
	return keys[0]
}

// activeSessionKey is the terminal session shown for the current context:
// the fullscreen-focused target when terminal focus is active, otherwise the
// entry selected in the tree.
func (s *tuiState) activeSessionKey() string {
	return s.targetActiveSessionKey(s.activeTerminalTargetKey())
}

func (s *tuiState) focusTree() {
	s.focus = tuiFocusTree
}

func (s *tuiState) toggleFocus() error {
	if s.focus == tuiFocusTerminal {
		s.focusTree()
		return nil
	}

	return s.focusTerminal()
}

func (s *tuiState) toggleTreePaneMode() {
	if s.treePaneMode == tuiTreePaneModeTerminal {
		s.treePaneMode = tuiTreePaneModeInfo
		return
	}

	s.treePaneMode = tuiTreePaneModeTerminal
}

func (s *tuiState) visibleEntries(capacity int) []tuiTreeEntry {
	if len(s.entries) == 0 || capacity <= 0 {
		return nil
	}

	start := s.viewportStart(capacity)
	end := start + capacity
	if end > len(s.entries) {
		end = len(s.entries)
	}
	return s.entries[start:end]
}

func (s *tuiState) selectTreeRow(row int, capacity int) error {
	index := s.viewportStart(capacity) + row
	if index < 0 || index >= len(s.entries) {
		return nil
	}

	return s.selectIndex(index)
}

func (s *tuiState) viewportStart(capacity int) int {
	if capacity <= 0 || len(s.entries) == 0 {
		return 0
	}

	if s.selection < s.scroll {
		s.scroll = s.selection
	}
	if s.selection >= s.scroll+capacity {
		s.scroll = s.selection - capacity + 1
	}

	if s.scroll < 0 {
		s.scroll = 0
	}

	maxScroll := len(s.entries) - capacity
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}

	return s.scroll
}

func (s *tuiState) activeNode() (Node, bool) {
	if entry := s.selectedEntry(); entry.valid() {
		return entry.node, true
	}

	target, err := terminal.ParseTargetKey(s.activeTerminalTargetKey())
	if err != nil || target.Kind != terminal.TargetNode {
		return Node{}, false
	}

	node, ok := s.nodesByID[target.ID]
	return node, ok
}

func (s *tuiState) replaceNodes(nodes []Node, preferredKey string) error {
	previous := s.selectedEntry()
	selectedKey := preferredKey
	if selectedKey == "" {
		selectedKey = previous.key()
	}
	s.nodes = append([]Node(nil), nodes...)
	s.indexNodes()
	s.rebuildEntries()

	selectIndex := func(index int) error {
		if err := s.selectIndex(index); err != nil {
			return err
		}
		s.applySelectedNodeDefault(previous)
		return nil
	}

	if selectedKey != "" {
		if index := s.findEntryByKey(selectedKey); index >= 0 {
			return selectIndex(index)
		}
	}

	if len(s.entries) == 0 {
		s.selection = -1
		s.terminalTarget = ""
		s.treePaneMode = tuiTreePaneModeInfo
		return nil
	}

	if s.selection < 0 || s.selection >= len(s.entries) {
		return selectIndex(0)
	}

	return selectIndex(s.selection)
}

func (s *tuiState) activeTerminalTargetKey() string {
	if s.focus == tuiFocusTerminal && s.terminalTarget != "" {
		return s.terminalTarget
	}
	if entry := s.selectedEntry(); entry.valid() {
		return entry.key()
	}
	return s.terminalTarget
}

func (s *tuiState) activeTerminalEntry() tuiTreeEntry {
	if entry, ok := s.entryForKey(s.activeTerminalTargetKey()); ok {
		return entry
	}
	return s.selectedEntry()
}

func (s *tuiState) entryForKey(key string) (tuiTreeEntry, bool) {
	if key == "" {
		return tuiTreeEntry{}, false
	}
	if index := s.findEntryByKey(key); index >= 0 {
		return s.entries[index], true
	}
	target, err := terminal.ParseTargetKey(key)
	if err != nil || target.Kind != terminal.TargetNode {
		return tuiTreeEntry{}, false
	}
	node, ok := s.nodesByID[target.ID]
	if !ok {
		return tuiTreeEntry{}, false
	}
	return tuiTreeEntry{node: node}, true
}

func availableTUIActions(entry tuiTreeEntry) []tuiActionSpec {
	actions := []tuiActionSpec{
		{ID: tuiActionNodeCreate, Label: "Create Node", Hotkey: 'n'},
		{ID: tuiActionConfigurationManage, Label: "Configurations", Hotkey: 'a'},
		{ID: tuiActionEnvironmentConfigManage, Label: "Environments", Hotkey: 'g'},
	}

	if !entry.valid() {
		return actions
	}

	if nodeIsRunning(entry.node) {
		actions = append(actions, tuiActionSpec{ID: tuiActionNodeStop, Label: "Stop Node", Hotkey: 's'})
	} else {
		actions = append(actions, tuiActionSpec{ID: tuiActionNodeStart, Label: "Start Node", Hotkey: 's'})
	}
	actions = append(actions,
		tuiActionSpec{ID: tuiActionNodeDelete, Label: "Delete Node", Hotkey: 'd'},
		tuiActionSpec{ID: tuiActionNodeClone, Label: "Clone Node", Hotkey: 'c'},
	)
	return actions
}

func nodeIsRunning(node Node) bool {
	if observation := node.LastRuntimeObservation; observation != nil && observation.Status == ObservationRunning {
		return true
	}
	return node.Status == NodeStatusRunning
}
