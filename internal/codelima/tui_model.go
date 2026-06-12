package codelima

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type TUIRunner interface {
	Run(ctx context.Context, service *Service) error
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

type tuiTreeEntryKind string

const (
	tuiTreeEntryProject tuiTreeEntryKind = "project"
	tuiTreeEntryNode    tuiTreeEntryKind = "node"
)

type tuiSessionManager interface {
	HasSession(sessionKey string) bool
	TargetSessionKeys(targetKey string) []string
	OpenProjectTab(project Project) (string, error)
	OpenNodeTab(node Node) (string, error)
}

type tuiActionID string

const (
	tuiActionProjectCreate           tuiActionID = "project.create"
	tuiActionEnvironmentConfigManage tuiActionID = "environment_config.manage"
	tuiActionProjectCreateNode       tuiActionID = "project.create_node"
	tuiActionProjectUpdate           tuiActionID = "project.update"
	tuiActionProjectDelete           tuiActionID = "project.delete"
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

func (tuiNoopSessionManager) OpenProjectTab(Project) (string, error) {
	return "", nil
}

func (tuiNoopSessionManager) OpenNodeTab(Node) (string, error) {
	return "", nil
}

type tuiTreeEntry struct {
	kind            tuiTreeEntryKind
	depth           int
	project         Project
	node            Node
	parentProjectID string
	expanded        bool
	hasChildren     bool
}

func (e tuiTreeEntry) key() string {
	switch e.kind {
	case tuiTreeEntryProject:
		return "project:" + e.project.ID
	case tuiTreeEntryNode:
		return "node:" + e.node.ID
	default:
		return ""
	}
}

type tuiState struct {
	tree                  []ProjectTreeNode
	entries               []tuiTreeEntry
	expanded              map[string]bool
	projectsByID          map[string]Project
	nodesByID             map[string]Node
	selection             int
	scroll                int
	focus                 tuiFocus
	treePaneMode          tuiTreePaneMode
	terminalTarget        string
	hostTerminalReturnKey string
	activeTabKeys         map[string]string
	sessions              tuiSessionManager
}

func newTUIState(tree []ProjectTreeNode, sessions tuiSessionManager) (*tuiState, error) {
	if sessions == nil {
		sessions = tuiNoopSessionManager{}
	}

	state := &tuiState{
		tree:          append([]ProjectTreeNode(nil), tree...),
		expanded:      map[string]bool{},
		projectsByID:  map[string]Project{},
		nodesByID:     map[string]Node{},
		selection:     -1,
		focus:         tuiFocusTree,
		treePaneMode:  tuiTreePaneModeInfo,
		activeTabKeys: map[string]string{},
		sessions:      sessions,
	}

	state.indexTree(tree)
	state.rebuildEntries()
	if len(state.entries) == 0 {
		return state, nil
	}

	initialSelection := state.firstNodeIndex()
	if initialSelection < 0 {
		initialSelection = 0
	}

	if err := state.selectIndex(initialSelection); err != nil {
		return nil, err
	}

	return state, nil
}

func (s *tuiState) indexTree(nodes []ProjectTreeNode) {
	for _, node := range nodes {
		s.projectsByID[node.Project.ID] = node.Project
		s.expanded[node.Project.ID] = true
		for _, projectNode := range node.Nodes {
			s.nodesByID[projectNode.ID] = projectNode
		}
		s.indexTree(node.Children)
	}
}

func (s *tuiState) rebuildEntries() {
	selectedKey := s.selectedEntry().key()
	entries := make([]tuiTreeEntry, 0)
	s.flattenTree(&entries, s.tree, 0, "")
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

func (s *tuiState) flattenTree(entries *[]tuiTreeEntry, nodes []ProjectTreeNode, depth int, parentProjectID string) {
	for _, projectNode := range nodes {
		expanded := s.expanded[projectNode.Project.ID]
		projectEntry := tuiTreeEntry{
			kind:            tuiTreeEntryProject,
			depth:           depth,
			project:         projectNode.Project,
			parentProjectID: parentProjectID,
			expanded:        expanded,
			hasChildren:     len(projectNode.Nodes) > 0 || len(projectNode.Children) > 0,
		}
		*entries = append(*entries, projectEntry)
		if !expanded {
			continue
		}

		for _, projectNodeChild := range projectNode.Nodes {
			*entries = append(*entries, tuiTreeEntry{
				kind:            tuiTreeEntryNode,
				depth:           depth + 1,
				project:         projectNode.Project,
				node:            projectNodeChild,
				parentProjectID: projectNode.Project.ID,
			})
		}

		s.flattenTree(entries, projectNode.Children, depth+1, projectNode.Project.ID)
	}
}

func (s *tuiState) selectedEntry() tuiTreeEntry {
	if s.selection < 0 || s.selection >= len(s.entries) {
		return tuiTreeEntry{}
	}

	return s.entries[s.selection]
}

func (s *tuiState) firstNodeIndex() int {
	for index, entry := range s.entries {
		if entry.kind == tuiTreeEntryNode {
			return index
		}
	}

	return -1
}

func (s *tuiState) findEntryByKey(key string) int {
	for index, entry := range s.entries {
		if entry.key() == key {
			return index
		}
	}

	return -1
}

func (s *tuiState) findProjectEntry(projectID string) int {
	return s.findEntryByKey("project:" + projectID)
}

func (s *tuiState) selectIndex(index int) error {
	if len(s.entries) == 0 {
		s.selection = -1
		return nil
	}

	if index < 0 {
		index = 0
	}
	if index >= len(s.entries) {
		index = len(s.entries) - 1
	}

	// Selecting or visiting an entry never creates or activates terminal
	// tabs; tabs are opened explicitly with the open-tab keybinding.
	s.selection = index
	return nil
}

func (s *tuiState) moveSelection(delta int) error {
	if len(s.entries) == 0 || delta == 0 {
		return nil
	}

	return s.selectIndex(s.selection + delta)
}

func (s *tuiState) collapseSelection() {
	entry := s.selectedEntry()
	switch entry.kind {
	case tuiTreeEntryNode:
		if index := s.findProjectEntry(entry.parentProjectID); index >= 0 {
			_ = s.selectIndex(index)
		}
	case tuiTreeEntryProject:
		if entry.expanded && entry.hasChildren {
			s.expanded[entry.project.ID] = false
			s.rebuildEntries()
			return
		}
		if entry.parentProjectID != "" {
			if index := s.findProjectEntry(entry.parentProjectID); index >= 0 {
				_ = s.selectIndex(index)
			}
		}
	}
}

func (s *tuiState) expandSelection() {
	entry := s.selectedEntry()
	if entry.kind != tuiTreeEntryProject || !entry.hasChildren || entry.expanded {
		return
	}

	s.expanded[entry.project.ID] = true
	s.rebuildEntries()
}

func (s *tuiState) focusTerminal() error {
	entry := s.selectedEntry()
	if entry.kind != tuiTreeEntryProject && entry.kind != tuiTreeEntryNode {
		return errors.New("select a project or node to focus the terminal")
	}

	s.hostTerminalReturnKey = ""
	return s.focusTerminalEntry(entry)
}

func (s *tuiState) focusTerminalEntry(entry tuiTreeEntry) error {
	if entry.kind != tuiTreeEntryProject && entry.kind != tuiTreeEntryNode {
		return errors.New("select a project or node to focus the terminal")
	}
	if entry.kind == tuiTreeEntryNode && !nodeAutoStartsSession(entry.node) {
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
		return "", errors.New("select a project or node to focus the terminal")
	}
	if sessionKey := s.targetActiveSessionKey(targetKey); sessionKey != "" {
		s.setActiveTab(targetKey, sessionKey)
		return sessionKey, nil
	}
	return s.openTerminalTabEntry(entry)
}

// openInitialTerminalTab makes one terminal tab available for the initial
// selection without changing tree focus or the info-first right pane mode.
func (s *tuiState) openInitialTerminalTab() error {
	entry := s.selectedEntry()
	if entry.kind != tuiTreeEntryProject && entry.kind != tuiTreeEntryNode {
		return nil
	}
	if s.targetActiveSessionKey(entry.key()) != "" {
		return nil
	}
	if entry.kind == tuiTreeEntryNode && !nodeAutoStartsSession(entry.node) {
		return nil
	}
	_, err := s.openTerminalTabEntry(entry)
	return err
}

// openTerminalTabEntry opens a fresh terminal tab for the entry and makes it
// the entry's active tab.
func (s *tuiState) openTerminalTabEntry(entry tuiTreeEntry) (string, error) {
	switch entry.kind {
	case tuiTreeEntryProject:
		sessionKey, err := s.sessions.OpenProjectTab(entry.project)
		if err != nil {
			return "", fmt.Errorf("start shell for %s: %w", entry.project.Slug, err)
		}
		s.setActiveTab(entry.key(), sessionKey)
		return sessionKey, nil
	case tuiTreeEntryNode:
		if !nodeAutoStartsSession(entry.node) {
			return "", errors.New("selected node is not running; start it before opening a terminal tab")
		}
		sessionKey, err := s.sessions.OpenNodeTab(entry.node)
		if err != nil {
			return "", fmt.Errorf("start shell for %s: %w", entry.node.Slug, err)
		}
		s.setActiveTab(entry.key(), sessionKey)
		return sessionKey, nil
	default:
		return "", errors.New("select a project or node to open a terminal tab")
	}
}

func (s *tuiState) setActiveTab(targetKey, sessionKey string) {
	if targetKey == "" || sessionKey == "" {
		return
	}
	s.activeTabKeys[targetKey] = sessionKey
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
	if active := s.activeTabKeys[targetKey]; active != "" && containsString(keys, active) {
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

func (s *tuiState) toggleHostTerminal() error {
	if s.hostTerminalReturnKey != "" && strings.HasPrefix(s.activeTerminalTargetKey(), "project:") {
		returnEntry, ok := s.entryForKey(s.hostTerminalReturnKey)
		s.hostTerminalReturnKey = ""
		if !ok {
			return errors.New("previous node terminal is no longer available")
		}
		return s.focusTerminalEntry(returnEntry)
	}

	nodeEntry, ok := s.activeOrSelectedNodeEntry()
	if !ok {
		entry := s.selectedEntry()
		if entry.kind == tuiTreeEntryProject {
			s.hostTerminalReturnKey = ""
			return s.focusTerminalEntry(entry)
		}
		return errors.New("select a node to switch between node and host terminals")
	}

	project, ok := s.projectsByID[nodeEntry.node.ProjectID]
	if !ok {
		project = nodeEntry.project
	}
	if strings.TrimSpace(project.ID) == "" {
		return errors.New("selected node project is no longer available")
	}

	s.hostTerminalReturnKey = nodeEntry.key()
	return s.focusTerminalEntry(tuiTreeEntry{kind: tuiTreeEntryProject, project: project})
}

func (s *tuiState) activeOrSelectedNodeEntry() (tuiTreeEntry, bool) {
	if entry, ok := s.entryForKey(s.activeTerminalTargetKey()); ok && entry.kind == tuiTreeEntryNode {
		return entry, true
	}
	entry := s.selectedEntry()
	if entry.kind == tuiTreeEntryNode {
		return entry, true
	}
	return tuiTreeEntry{}, false
}

func (s *tuiState) toggleTreePaneMode() error {
	if s.treePaneMode == tuiTreePaneModeTerminal {
		s.treePaneMode = tuiTreePaneModeInfo
		return nil
	}

	s.treePaneMode = tuiTreePaneModeTerminal
	return nil
}

func (s *tuiState) visibleEntries(height int) []tuiTreeEntry {
	if len(s.entries) == 0 || height <= 0 {
		return nil
	}

	start := s.viewportStart(height)
	end := start + height
	if end > len(s.entries) {
		end = len(s.entries)
	}
	return s.entries[start:end]
}

func (s *tuiState) selectTreeRow(row int, height int) error {
	index := s.viewportStart(height) + row
	if index < 0 || index >= len(s.entries) {
		return nil
	}

	return s.selectIndex(index)
}

func (s *tuiState) viewportStart(height int) int {
	if height <= 0 || len(s.entries) == 0 {
		return 0
	}

	if s.selection < s.scroll {
		s.scroll = s.selection
	}
	if s.selection >= s.scroll+height {
		s.scroll = s.selection - height + 1
	}

	if s.scroll < 0 {
		s.scroll = 0
	}

	maxScroll := len(s.entries) - height
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}

	return s.scroll
}

func (s *tuiState) activeNode() (Node, bool) {
	if entry := s.selectedEntry(); entry.kind == tuiTreeEntryNode {
		return entry.node, true
	}

	targetKey := s.activeTerminalTargetKey()
	if !strings.HasPrefix(targetKey, "node:") {
		return Node{}, false
	}

	node, ok := s.nodesByID[strings.TrimPrefix(targetKey, "node:")]
	return node, ok
}

func (s *tuiState) activeProject() (Project, bool) {
	entry := s.selectedEntry()
	switch entry.kind {
	case tuiTreeEntryProject:
		return entry.project, true
	case tuiTreeEntryNode:
		return entry.project, true
	}

	if targetKey := s.activeTerminalTargetKey(); strings.HasPrefix(targetKey, "project:") {
		project, ok := s.projectsByID[strings.TrimPrefix(targetKey, "project:")]
		return project, ok
	}
	if node, ok := s.activeNode(); ok {
		project, found := s.projectsByID[node.ProjectID]
		return project, found
	}

	return Project{}, false
}

func (s *tuiState) replaceTree(tree []ProjectTreeNode, preferredKey string) error {
	selectedKey := preferredKey
	if selectedKey == "" {
		selectedKey = s.selectedEntry().key()
	}
	hostOverrideActive := s.hostTerminalReturnKey != "" && strings.HasPrefix(s.terminalTarget, "project:")
	hostOverrideActiveKey := s.terminalTarget
	hostOverrideReturnKey := s.hostTerminalReturnKey
	restoreHostOverride := func() {
		if !hostOverrideActive {
			return
		}
		if _, ok := s.entryForKey(hostOverrideActiveKey); !ok {
			return
		}
		if _, ok := s.entryForKey(hostOverrideReturnKey); !ok {
			return
		}
		s.terminalTarget = hostOverrideActiveKey
		s.hostTerminalReturnKey = hostOverrideReturnKey
	}

	expanded := cloneExpandedState(s.expanded)
	s.tree = append([]ProjectTreeNode(nil), tree...)
	s.expanded = expanded
	s.projectsByID = map[string]Project{}
	s.nodesByID = map[string]Node{}
	s.indexTree(tree)
	s.rebuildEntries()

	if selectedKey != "" {
		if index := s.findEntryByKey(selectedKey); index >= 0 {
			err := s.selectIndex(index)
			restoreHostOverride()
			return err
		}
		if s.expandToKey(selectedKey) {
			s.rebuildEntries()
		}
		if index := s.findEntryByKey(selectedKey); index >= 0 {
			err := s.selectIndex(index)
			restoreHostOverride()
			return err
		}
	}

	if len(s.entries) == 0 {
		s.selection = -1
		s.terminalTarget = ""
		s.hostTerminalReturnKey = ""
		return nil
	}

	if s.selection < 0 || s.selection >= len(s.entries) {
		err := s.selectIndex(0)
		restoreHostOverride()
		return err
	}

	err := s.selectIndex(s.selection)
	restoreHostOverride()
	return err
}

func (s *tuiState) activeTerminalTargetKey() string {
	if s.focus == tuiFocusTerminal && s.terminalTarget != "" {
		return s.terminalTarget
	}
	if entry := s.selectedEntry(); entry.kind == tuiTreeEntryProject || entry.kind == tuiTreeEntryNode {
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
	if strings.HasPrefix(key, "project:") {
		project, ok := s.projectsByID[strings.TrimPrefix(key, "project:")]
		if !ok {
			return tuiTreeEntry{}, false
		}
		return tuiTreeEntry{kind: tuiTreeEntryProject, project: project}, true
	}
	if strings.HasPrefix(key, "node:") {
		node, ok := s.nodesByID[strings.TrimPrefix(key, "node:")]
		if !ok {
			return tuiTreeEntry{}, false
		}
		project := s.projectsByID[node.ProjectID]
		return tuiTreeEntry{kind: tuiTreeEntryNode, project: project, node: node, parentProjectID: node.ProjectID}, true
	}
	return tuiTreeEntry{}, false
}

func availableTUIActions(entry tuiTreeEntry) []tuiActionSpec {
	actions := []tuiActionSpec{
		{ID: tuiActionProjectCreate, Label: "Add Project", Hotkey: 'a'},
		{ID: tuiActionEnvironmentConfigManage, Label: "Env Configs", Hotkey: 'g'},
	}

	switch entry.kind {
	case tuiTreeEntryProject:
		actions = append(actions,
			tuiActionSpec{ID: tuiActionProjectCreateNode, Label: "Create Node", Hotkey: 'n'},
			tuiActionSpec{ID: tuiActionProjectUpdate, Label: "Update Project", Hotkey: 'u'},
			tuiActionSpec{ID: tuiActionProjectDelete, Label: "Delete Project", Hotkey: 'x'},
		)
		return actions
	case tuiTreeEntryNode:
		if nodeAutoStartsSession(entry.node) {
			actions = append(actions, tuiActionSpec{ID: tuiActionNodeStop, Label: "Stop Node", Hotkey: 's'})
		} else {
			actions = append(actions, tuiActionSpec{ID: tuiActionNodeStart, Label: "Start Node", Hotkey: 's'})
		}
		actions = append(actions,
			tuiActionSpec{ID: tuiActionNodeDelete, Label: "Delete Node", Hotkey: 'd'},
			tuiActionSpec{ID: tuiActionNodeClone, Label: "Clone Node", Hotkey: 'c'},
		)
		return actions
	default:
		return actions
	}
}

func nodeAutoStartsSession(node Node) bool {
	return nodeVMStatus(node) == "running" || node.Status == NodeStatusRunning
}

func cloneExpandedState(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return map[string]bool{}
	}

	target := make(map[string]bool, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func (s *tuiState) expandToKey(key string) bool {
	projectIDs, ok := projectLineageForKey(s.tree, key, nil)
	if !ok {
		return false
	}

	for _, projectID := range projectIDs {
		s.expanded[projectID] = true
	}
	return true
}

func projectLineageForKey(nodes []ProjectTreeNode, key string, path []string) ([]string, bool) {
	for _, projectNode := range nodes {
		nextPath := append(append([]string(nil), path...), projectNode.Project.ID)
		if key == "project:"+projectNode.Project.ID {
			return nextPath, true
		}
		for _, childNode := range projectNode.Nodes {
			if key == "node:"+childNode.ID {
				return nextPath, true
			}
		}
		if lineage, ok := projectLineageForKey(projectNode.Children, key, nextPath); ok {
			return lineage, true
		}
	}

	return nil, false
}
