package codelima

import (
	"encoding/json"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	RuntimeVM        = "vm"
	RuntimeContainer = "container"

	ProviderMicrosandbox = "microsandbox"

	WorkspaceModeCopy    = "copy"
	WorkspaceModeMounted = "mounted"
	DefaultWorkspaceMode = WorkspaceModeMounted

	NodeStatusCreated      = "created"
	NodeStatusProvisioning = "provisioning"
	NodeStatusRegistering  = "registering"
	NodeStatusRunning      = "running"
	NodeStatusStopped      = "stopped"
	NodeStatusFailed       = "failed"
	NodeStatusTerminating  = "terminating"
	NodeStatusTerminated   = "terminated"

	DefaultConfigurationSlug = "default"
	DefaultVCPUs             = uint8(2)
	DefaultMemoryMiB         = uint32(4 * 1024)
	DefaultDiskMiB           = uint32(20 * 1024)
)

type RuntimeCommandTemplates struct {
	Version              []string `json:"version,omitempty" yaml:"version,omitempty"`
	List                 []string `json:"list,omitempty" yaml:"list,omitempty"`
	Create               []string `json:"create,omitempty" yaml:"create,omitempty"`
	Start                []string `json:"start,omitempty" yaml:"start,omitempty"`
	Stop                 []string `json:"stop,omitempty" yaml:"stop,omitempty"`
	Delete               []string `json:"delete,omitempty" yaml:"delete,omitempty"`
	Clone                []string `json:"clone,omitempty" yaml:"clone,omitempty"`
	Bootstrap            []string `json:"bootstrap,omitempty" yaml:"bootstrap,omitempty"`
	WorkspaceSeedPrepare []string `json:"workspace_seed_prepare,omitempty" yaml:"workspace_seed_prepare,omitempty"`
	Copy                 []string `json:"copy,omitempty" yaml:"copy,omitempty"`
	ShellExec            []string `json:"shell_exec,omitempty" yaml:"shell_exec,omitempty"`
	ShellLogin           []string `json:"shell_login,omitempty" yaml:"shell_login,omitempty"`
}

type NetPolicy struct {
	Default string   `json:"default" yaml:"default"`
	Allow   []string `json:"allow,omitempty" yaml:"allow,omitempty"`
}

// Configuration is a reusable sandbox recipe. Nodes copy its resolved values
// when they are created, so later configuration edits affect future nodes only.
// A configuration deliberately has no host-directory field: directory identity
// belongs to a node.
type Configuration struct {
	ID                string     `json:"id" yaml:"id"`
	Slug              string     `json:"slug" yaml:"slug"`
	Image             string     `json:"image" yaml:"image"`
	AgentProfileName  string     `json:"agent_profile_name" yaml:"agent_profile_name"`
	Environments      []string   `json:"environments" yaml:"environments"`
	BootstrapCommands []string   `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	VCPUs             uint8      `json:"vcpus" yaml:"vcpus"`
	MemoryMiB         uint32     `json:"memory_mib" yaml:"memory_mib"`
	DiskMiB           uint32     `json:"disk_mib" yaml:"disk_mib"`
	CreatedAt         time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" yaml:"updated_at"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
}

type Project struct {
	ID                 string                  `json:"id" yaml:"id"`
	Slug               string                  `json:"slug" yaml:"slug"`
	WorkspacePath      string                  `json:"workspace_path" yaml:"workspace_path"`
	ParentProjectID    string                  `json:"parent_project_id,omitempty" yaml:"parent_project_id,omitempty"`
	ForkBaseSnapshotID string                  `json:"fork_base_snapshot_id,omitempty" yaml:"fork_base_snapshot_id,omitempty"`
	AgentProfileName   string                  `json:"agent_profile_name" yaml:"agent_profile_name"`
	EnvironmentConfigs []string                `json:"environment_configs" yaml:"environment_configs"`
	DefaultRuntime     string                  `json:"default_runtime" yaml:"default_runtime"`
	DefaultProvider    string                  `json:"default_provider" yaml:"default_provider"`
	DefaultImage       string                  `json:"default_image" yaml:"default_image"`
	DefaultPorts       []string                `json:"default_ports,omitempty" yaml:"default_ports,omitempty"`
	RuntimeCommands    RuntimeCommandTemplates `json:"runtime_commands,omitempty" yaml:"runtime_commands,omitempty"`
	CreatedAt          time.Time               `json:"created_at" yaml:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at" yaml:"updated_at"`
	DeletedAt          *time.Time              `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
}

type EnvironmentConfig struct {
	ID                string     `json:"id" yaml:"id"`
	Slug              string     `json:"slug" yaml:"slug"`
	BootstrapCommands []string   `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	CreatedAt         time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" yaml:"updated_at"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
}

// Environment is the public name for a reusable bootstrap bundle. Keep the
// alias while older internal call sites are retired; serialized data and the
// v3 layout use the concise "environment" terminology.
type Environment = EnvironmentConfig

type RuntimeObservation struct {
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	Exists   bool   `json:"exists" yaml:"exists"`
	Status   string `json:"status,omitempty" yaml:"status,omitempty"`
	Dir      string `json:"dir,omitempty" yaml:"dir,omitempty"`
	Hostname string `json:"hostname,omitempty" yaml:"hostname,omitempty"`
}

type Node struct {
	ID                string `json:"id" yaml:"id"`
	Slug              string `json:"slug" yaml:"slug"`
	ConfigurationID   string `json:"configuration_id" yaml:"configuration_id"`
	ConfigurationSlug string `json:"configuration_slug,omitempty" yaml:"configuration_slug,omitempty"`
	DirectoryPath     string `json:"directory_path" yaml:"directory_path"`
	// ProjectID is retained only for source compatibility with pre-v3 internal
	// tests and is never populated or serialized for v3 nodes.
	ProjectID              string                  `json:"-" yaml:"-"`
	ParentNodeID           string                  `json:"parent_node_id,omitempty" yaml:"parent_node_id,omitempty"`
	Runtime                string                  `json:"runtime" yaml:"runtime"`
	Provider               string                  `json:"provider" yaml:"provider"`
	SandboxName            string                  `json:"sandbox_name" yaml:"sandbox_name"`
	Image                  string                  `json:"image" yaml:"image"`
	VCPUs                  uint8                   `json:"vcpus" yaml:"vcpus"`
	MemoryMiB              uint32                  `json:"memory_mib" yaml:"memory_mib"`
	DiskMiB                uint32                  `json:"disk_mib" yaml:"disk_mib"`
	Environments           []string                `json:"environments" yaml:"environments"`
	Ports                  []string                `json:"ports,omitempty" yaml:"ports,omitempty"`
	NetPolicy              *NetPolicy              `json:"net_policy,omitempty" yaml:"net_policy,omitempty"`
	Status                 string                  `json:"status" yaml:"status"`
	LifecycleState         string                  `json:"-" yaml:"-"`
	AgentProfileName       string                  `json:"agent_profile_name" yaml:"agent_profile_name"`
	RuntimeCommands        RuntimeCommandTemplates `json:"runtime_commands,omitempty" yaml:"runtime_commands,omitempty"`
	BootstrapCommands      []string                `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	WorkspaceMode          string                  `json:"workspace_mode,omitempty" yaml:"workspace_mode,omitempty"`
	GuestWorkspacePath     string                  `json:"guest_workspace_path,omitempty" yaml:"guest_workspace_path,omitempty"`
	WorkspaceMountPath     string                  `json:"workspace_mount_path,omitempty" yaml:"workspace_mount_path,omitempty"`
	WorkspaceSeeded        bool                    `json:"workspace_seeded" yaml:"workspace_seeded"`
	BootstrapCompleted     bool                    `json:"bootstrap_completed" yaml:"bootstrap_completed"`
	BootstrapCompletedAt   *time.Time              `json:"bootstrap_completed_at,omitempty" yaml:"bootstrap_completed_at,omitempty"`
	CreatedAt              time.Time               `json:"created_at" yaml:"created_at"`
	UpdatedAt              time.Time               `json:"updated_at" yaml:"updated_at"`
	DeletedAt              *time.Time              `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
	LastReconciledAt       *time.Time              `json:"last_reconciled_at,omitempty" yaml:"last_reconciled_at,omitempty"`
	LastRuntimeObservation *RuntimeObservation     `json:"last_runtime_observation,omitempty" yaml:"last_runtime_observation,omitempty"`
}

type nodeFileWire struct {
	ID                   string                  `json:"id" yaml:"id"`
	Slug                 string                  `json:"slug" yaml:"slug"`
	ConfigurationID      string                  `json:"configuration_id" yaml:"configuration_id"`
	DirectoryPath        string                  `json:"directory_path" yaml:"directory_path"`
	ProjectID            string                  `json:"project_id,omitempty" yaml:"project_id,omitempty"`
	ParentNodeID         string                  `json:"parent_node_id,omitempty" yaml:"parent_node_id,omitempty"`
	Runtime              string                  `json:"runtime" yaml:"runtime"`
	Provider             string                  `json:"provider" yaml:"provider"`
	SandboxName          string                  `json:"sandbox_name" yaml:"sandbox_name"`
	Image                string                  `json:"image" yaml:"image"`
	VCPUs                uint8                   `json:"vcpus" yaml:"vcpus"`
	MemoryMiB            uint32                  `json:"memory_mib" yaml:"memory_mib"`
	DiskMiB              uint32                  `json:"disk_mib" yaml:"disk_mib"`
	Environments         []string                `json:"environments" yaml:"environments"`
	Ports                []string                `json:"ports,omitempty" yaml:"ports,omitempty"`
	NetPolicy            *NetPolicy              `json:"net_policy,omitempty" yaml:"net_policy,omitempty"`
	LifecycleState       string                  `json:"lifecycle_state,omitempty" yaml:"lifecycle_state,omitempty"`
	Status               string                  `json:"status,omitempty" yaml:"status,omitempty"`
	AgentProfileName     string                  `json:"agent_profile_name" yaml:"agent_profile_name"`
	RuntimeCommands      RuntimeCommandTemplates `json:"runtime_commands,omitempty" yaml:"runtime_commands,omitempty"`
	BootstrapCommands    []string                `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	WorkspaceMode        string                  `json:"workspace_mode,omitempty" yaml:"workspace_mode,omitempty"`
	GuestWorkspacePath   string                  `json:"guest_workspace_path,omitempty" yaml:"guest_workspace_path,omitempty"`
	WorkspaceMountPath   string                  `json:"workspace_mount_path,omitempty" yaml:"workspace_mount_path,omitempty"`
	WorkspaceSeeded      bool                    `json:"workspace_seeded" yaml:"workspace_seeded"`
	BootstrapCompleted   bool                    `json:"bootstrap_completed" yaml:"bootstrap_completed"`
	BootstrapCompletedAt *time.Time              `json:"bootstrap_completed_at,omitempty" yaml:"bootstrap_completed_at,omitempty"`
	CreatedAt            time.Time               `json:"created_at" yaml:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at" yaml:"updated_at"`
	DeletedAt            *time.Time              `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
}

func normalizeNodeLifecycleState(state string) string {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case NodeStatusCreated,
		NodeStatusProvisioning,
		NodeStatusRegistering,
		NodeStatusFailed,
		NodeStatusTerminating,
		NodeStatusTerminated:
		return strings.TrimSpace(strings.ToLower(state))
	default:
		return ""
	}
}

func legacyNodeStatus(state string) string {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case NodeStatusCreated,
		NodeStatusProvisioning,
		NodeStatusRegistering,
		NodeStatusRunning,
		NodeStatusStopped,
		NodeStatusFailed,
		NodeStatusTerminating,
		NodeStatusTerminated:
		return strings.TrimSpace(strings.ToLower(state))
	default:
		return ""
	}
}

func nodeLifecycleState(node Node) string {
	if state := normalizeNodeLifecycleState(node.Status); state != "" {
		return state
	}

	if !node.BootstrapCompleted {
		return normalizeNodeLifecycleState(node.LifecycleState)
	}

	return ""
}

func newNodeFileWire(node Node) nodeFileWire {
	return nodeFileWire{
		ID:                   node.ID,
		Slug:                 node.Slug,
		ConfigurationID:      node.ConfigurationID,
		DirectoryPath:        node.DirectoryPath,
		ProjectID:            node.ProjectID,
		ParentNodeID:         node.ParentNodeID,
		Runtime:              node.Runtime,
		Provider:             node.Provider,
		SandboxName:          node.SandboxName,
		Image:                node.Image,
		VCPUs:                node.VCPUs,
		MemoryMiB:            node.MemoryMiB,
		DiskMiB:              node.DiskMiB,
		Environments:         append([]string(nil), node.Environments...),
		Ports:                append([]string(nil), node.Ports...),
		NetPolicy:            cloneNetPolicy(node.NetPolicy),
		LifecycleState:       nodeLifecycleState(node),
		AgentProfileName:     node.AgentProfileName,
		RuntimeCommands:      node.RuntimeCommands,
		BootstrapCommands:    append([]string(nil), node.BootstrapCommands...),
		WorkspaceMode:        node.WorkspaceMode,
		GuestWorkspacePath:   node.GuestWorkspacePath,
		WorkspaceMountPath:   node.WorkspaceMountPath,
		WorkspaceSeeded:      node.WorkspaceSeeded,
		BootstrapCompleted:   node.BootstrapCompleted,
		BootstrapCompletedAt: node.BootstrapCompletedAt,
		CreatedAt:            node.CreatedAt,
		UpdatedAt:            node.UpdatedAt,
		DeletedAt:            node.DeletedAt,
	}
}

func (w nodeFileWire) node() Node {
	lifecycleState := normalizeNodeLifecycleState(w.LifecycleState)
	legacyStatus := legacyNodeStatus(w.Status)
	if lifecycleState == "" {
		lifecycleState = normalizeNodeLifecycleState(legacyStatus)
	}

	status := lifecycleState
	if status == "" {
		status = legacyStatus
	}

	return Node{
		ID:                   w.ID,
		Slug:                 w.Slug,
		ConfigurationID:      w.ConfigurationID,
		DirectoryPath:        w.DirectoryPath,
		ProjectID:            w.ProjectID,
		ParentNodeID:         w.ParentNodeID,
		Runtime:              w.Runtime,
		Provider:             w.Provider,
		SandboxName:          w.SandboxName,
		Image:                w.Image,
		VCPUs:                w.VCPUs,
		MemoryMiB:            w.MemoryMiB,
		DiskMiB:              w.DiskMiB,
		Environments:         append([]string(nil), w.Environments...),
		Ports:                append([]string(nil), w.Ports...),
		NetPolicy:            cloneNetPolicy(w.NetPolicy),
		Status:               status,
		LifecycleState:       lifecycleState,
		AgentProfileName:     w.AgentProfileName,
		RuntimeCommands:      removeLegacyMSBCommandTemplates(w.RuntimeCommands),
		BootstrapCommands:    append([]string(nil), w.BootstrapCommands...),
		WorkspaceMode:        w.WorkspaceMode,
		GuestWorkspacePath:   w.GuestWorkspacePath,
		WorkspaceMountPath:   w.WorkspaceMountPath,
		WorkspaceSeeded:      w.WorkspaceSeeded,
		BootstrapCompleted:   w.BootstrapCompleted,
		BootstrapCompletedAt: w.BootstrapCompletedAt,
		CreatedAt:            w.CreatedAt,
		UpdatedAt:            w.UpdatedAt,
		DeletedAt:            w.DeletedAt,
	}
}

func cloneNetPolicy(policy *NetPolicy) *NetPolicy {
	if policy == nil {
		return nil
	}
	return &NetPolicy{Default: policy.Default, Allow: append([]string(nil), policy.Allow...)}
}

type BootstrapState struct {
	AgentProfileName  string            `json:"agent_profile_name" yaml:"agent_profile_name"`
	InstallCommands   []string          `json:"install_commands" yaml:"install_commands"`
	BootstrapCommands []string          `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	ValidationCommand string            `json:"validation_command" yaml:"validation_command"`
	LaunchCommand     string            `json:"launch_command" yaml:"launch_command"`
	Environment       map[string]string `json:"environment" yaml:"environment"`
	Completed         bool              `json:"completed" yaml:"completed"`
	CompletedAt       *time.Time        `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
}

func (b BootstrapState) CombinedCommands() []string {
	commands := make([]string, 0, len(b.InstallCommands)+len(b.BootstrapCommands))
	commands = append(commands, b.InstallCommands...)
	commands = append(commands, b.BootstrapCommands...)
	return commands
}

type AgentProfile struct {
	Name              string            `json:"name" yaml:"name"`
	InstallCommands   []string          `json:"install_commands" yaml:"install_commands"`
	ValidationCommand string            `json:"validation_command" yaml:"validation_command"`
	LaunchCommand     string            `json:"launch_command" yaml:"launch_command"`
	Environment       map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
}

type SnapshotManifest struct {
	ID            string          `json:"id" yaml:"id"`
	ProjectID     string          `json:"project_id" yaml:"project_id"`
	Kind          string          `json:"kind" yaml:"kind"`
	CreatedAt     time.Time       `json:"created_at" yaml:"created_at"`
	WorkspacePath string          `json:"workspace_path" yaml:"workspace_path"`
	EntryCount    int             `json:"entry_count" yaml:"entry_count"`
	TotalBytes    int64           `json:"total_bytes" yaml:"total_bytes"`
	Entries       []SnapshotEntry `json:"entries" yaml:"entries"`
	TreeRoot      string          `json:"tree_root" yaml:"tree_root"`
}

type SnapshotEntry struct {
	Path       string `json:"path" yaml:"path"`
	Type       string `json:"type" yaml:"type"`
	Mode       uint32 `json:"mode" yaml:"mode"`
	Size       int64  `json:"size,omitempty" yaml:"size,omitempty"`
	SHA256     string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	LinkTarget string `json:"link_target,omitempty" yaml:"link_target,omitempty"`
}

type DoctorReport struct {
	Checks   []DoctorCheck `json:"checks"`
	Warnings []string      `json:"warnings"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type IncompleteNodeMetadata struct {
	NodeID          string `json:"node_id" yaml:"node_id"`
	DirectoryPath   string `json:"directory_path" yaml:"directory_path"`
	TemplatePath    string `json:"template_path,omitempty" yaml:"template_path,omitempty"`
	SandboxName     string `json:"sandbox_name,omitempty" yaml:"sandbox_name,omitempty"`
	InstanceRefPath string `json:"instance_ref_path,omitempty" yaml:"instance_ref_path,omitempty"`
}

type IncompleteNodeCleanupResult struct {
	DryRun bool                     `json:"dry_run" yaml:"dry_run"`
	Items  []IncompleteNodeMetadata `json:"items" yaml:"items"`
}

type ProjectTreeNode struct {
	Project  Project           `json:"project"`
	Nodes    []Node            `json:"nodes,omitempty"`
	Children []ProjectTreeNode `json:"children"`
}

type projectWire struct {
	ID                  string                  `json:"id" yaml:"id"`
	Slug                string                  `json:"slug" yaml:"slug"`
	WorkspacePath       string                  `json:"workspace_path" yaml:"workspace_path"`
	ParentProjectID     string                  `json:"parent_project_id,omitempty" yaml:"parent_project_id,omitempty"`
	ForkBaseSnapshotID  string                  `json:"fork_base_snapshot_id,omitempty" yaml:"fork_base_snapshot_id,omitempty"`
	AgentProfileName    string                  `json:"agent_profile_name" yaml:"agent_profile_name"`
	EnvironmentConfigs  []string                `json:"environment_configs" yaml:"environment_configs"`
	EnvironmentCommands []string                `json:"environment_commands,omitempty" yaml:"environment_commands,omitempty"`
	SetupCommands       []string                `json:"setup_commands,omitempty" yaml:"setup_commands,omitempty"`
	DefaultRuntime      string                  `json:"default_runtime" yaml:"default_runtime"`
	DefaultProvider     string                  `json:"default_provider" yaml:"default_provider"`
	DefaultImage        string                  `json:"default_image" yaml:"default_image"`
	DefaultPorts        []string                `json:"default_ports,omitempty" yaml:"default_ports,omitempty"`
	RuntimeCommands     RuntimeCommandTemplates `json:"runtime_commands,omitempty" yaml:"runtime_commands,omitempty"`
	CreatedAt           time.Time               `json:"created_at" yaml:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at" yaml:"updated_at"`
	DeletedAt           *time.Time              `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
}

func (p Project) MarshalJSON() ([]byte, error) {
	return json.Marshal(newProjectWire(p))
}

func (p *Project) UnmarshalJSON(data []byte) error {
	var wire projectWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*p = wire.project()
	return nil
}

func (p Project) MarshalYAML() (any, error) {
	return newProjectWire(p), nil
}

func (p *Project) UnmarshalYAML(node *yaml.Node) error {
	var wire projectWire
	if err := node.Decode(&wire); err != nil {
		return err
	}

	*p = wire.project()
	return nil
}

func newProjectWire(project Project) projectWire {
	return projectWire{
		ID:                 project.ID,
		Slug:               project.Slug,
		WorkspacePath:      project.WorkspacePath,
		ParentProjectID:    project.ParentProjectID,
		ForkBaseSnapshotID: project.ForkBaseSnapshotID,
		AgentProfileName:   project.AgentProfileName,
		EnvironmentConfigs: append([]string(nil), project.EnvironmentConfigs...),
		DefaultRuntime:     project.DefaultRuntime,
		DefaultProvider:    project.DefaultProvider,
		DefaultImage:       project.DefaultImage,
		DefaultPorts:       append([]string(nil), project.DefaultPorts...),
		RuntimeCommands:    project.RuntimeCommands,
		CreatedAt:          project.CreatedAt,
		UpdatedAt:          project.UpdatedAt,
		DeletedAt:          project.DeletedAt,
	}
}

func (w projectWire) project() Project {
	project := Project{
		ID:                 w.ID,
		Slug:               w.Slug,
		WorkspacePath:      w.WorkspacePath,
		ParentProjectID:    w.ParentProjectID,
		ForkBaseSnapshotID: w.ForkBaseSnapshotID,
		AgentProfileName:   w.AgentProfileName,
		EnvironmentConfigs: append([]string(nil), w.EnvironmentConfigs...),
		DefaultRuntime:     w.DefaultRuntime,
		DefaultProvider:    w.DefaultProvider,
		DefaultImage:       w.DefaultImage,
		DefaultPorts:       append([]string(nil), w.DefaultPorts...),
		RuntimeCommands:    removeLegacyMSBCommandTemplates(w.RuntimeCommands),
		CreatedAt:          w.CreatedAt,
		UpdatedAt:          w.UpdatedAt,
		DeletedAt:          w.DeletedAt,
	}

	if len(project.RuntimeCommands.Bootstrap) == 0 {
		project.RuntimeCommands.Bootstrap = commandSliceWithLegacy(nil, w.EnvironmentCommands, w.SetupCommands)
	}

	return project
}

type bootstrapStateWire struct {
	AgentProfileName    string            `json:"agent_profile_name" yaml:"agent_profile_name"`
	InstallCommands     []string          `json:"install_commands" yaml:"install_commands"`
	BootstrapCommands   []string          `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	EnvironmentCommands []string          `json:"environment_commands,omitempty" yaml:"environment_commands,omitempty"`
	SetupCommands       []string          `json:"setup_commands,omitempty" yaml:"setup_commands,omitempty"`
	ValidationCommand   string            `json:"validation_command" yaml:"validation_command"`
	LaunchCommand       string            `json:"launch_command" yaml:"launch_command"`
	Environment         map[string]string `json:"environment" yaml:"environment"`
	Completed           bool              `json:"completed" yaml:"completed"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
}

func (b BootstrapState) MarshalJSON() ([]byte, error) {
	return json.Marshal(newBootstrapStateWire(b))
}

func (b *BootstrapState) UnmarshalJSON(data []byte) error {
	var wire bootstrapStateWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*b = wire.bootstrapState()
	return nil
}

func (b BootstrapState) MarshalYAML() (any, error) {
	return newBootstrapStateWire(b), nil
}

func (b *BootstrapState) UnmarshalYAML(node *yaml.Node) error {
	var wire bootstrapStateWire
	if err := node.Decode(&wire); err != nil {
		return err
	}

	*b = wire.bootstrapState()
	return nil
}

func newBootstrapStateWire(state BootstrapState) bootstrapStateWire {
	return bootstrapStateWire{
		AgentProfileName:  state.AgentProfileName,
		InstallCommands:   append([]string(nil), state.InstallCommands...),
		BootstrapCommands: append([]string(nil), state.BootstrapCommands...),
		ValidationCommand: state.ValidationCommand,
		LaunchCommand:     state.LaunchCommand,
		Environment:       cloneStringMap(state.Environment),
		Completed:         state.Completed,
		CompletedAt:       state.CompletedAt,
	}
}

func (w bootstrapStateWire) bootstrapState() BootstrapState {
	return BootstrapState{
		AgentProfileName:  w.AgentProfileName,
		InstallCommands:   append([]string(nil), w.InstallCommands...),
		BootstrapCommands: commandSliceWithLegacy(w.BootstrapCommands, w.EnvironmentCommands, w.SetupCommands),
		ValidationCommand: w.ValidationCommand,
		LaunchCommand:     w.LaunchCommand,
		Environment:       cloneStringMap(w.Environment),
		Completed:         w.Completed,
		CompletedAt:       w.CompletedAt,
	}
}

type environmentConfigWire struct {
	ID                  string     `json:"id" yaml:"id"`
	Slug                string     `json:"slug" yaml:"slug"`
	BootstrapCommands   []string   `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	EnvironmentCommands []string   `json:"environment_commands,omitempty" yaml:"environment_commands,omitempty"`
	SetupCommands       []string   `json:"setup_commands,omitempty" yaml:"setup_commands,omitempty"`
	CreatedAt           time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" yaml:"updated_at"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
}

func (c EnvironmentConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(environmentConfigWire{
		ID:                c.ID,
		Slug:              c.Slug,
		BootstrapCommands: append([]string(nil), c.BootstrapCommands...),
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
		DeletedAt:         c.DeletedAt,
	})
}

func (c *EnvironmentConfig) UnmarshalJSON(data []byte) error {
	var wire environmentConfigWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*c = wire.environmentConfig()
	return nil
}

func (c EnvironmentConfig) MarshalYAML() (any, error) {
	return environmentConfigWire{
		ID:                c.ID,
		Slug:              c.Slug,
		BootstrapCommands: append([]string(nil), c.BootstrapCommands...),
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
		DeletedAt:         c.DeletedAt,
	}, nil
}

func (c *EnvironmentConfig) UnmarshalYAML(node *yaml.Node) error {
	var wire environmentConfigWire
	if err := node.Decode(&wire); err != nil {
		return err
	}

	*c = wire.environmentConfig()
	return nil
}

func (w environmentConfigWire) environmentConfig() EnvironmentConfig {
	return EnvironmentConfig{
		ID:                w.ID,
		Slug:              w.Slug,
		BootstrapCommands: commandSliceWithLegacy(w.BootstrapCommands, w.EnvironmentCommands, w.SetupCommands),
		CreatedAt:         w.CreatedAt,
		UpdatedAt:         w.UpdatedAt,
		DeletedAt:         w.DeletedAt,
	}
}

type runtimeCommandTemplatesWire struct {
	Version              commandList `json:"version,omitempty" yaml:"version,omitempty"`
	List                 commandList `json:"list,omitempty" yaml:"list,omitempty"`
	Create               commandList `json:"create,omitempty" yaml:"create,omitempty"`
	Start                commandList `json:"start,omitempty" yaml:"start,omitempty"`
	Stop                 commandList `json:"stop,omitempty" yaml:"stop,omitempty"`
	Delete               commandList `json:"delete,omitempty" yaml:"delete,omitempty"`
	Clone                commandList `json:"clone,omitempty" yaml:"clone,omitempty"`
	Bootstrap            commandList `json:"bootstrap" yaml:"bootstrap"`
	WorkspaceSeedPrepare commandList `json:"workspace_seed_prepare,omitempty" yaml:"workspace_seed_prepare,omitempty"`
	Copy                 commandList `json:"copy,omitempty" yaml:"copy,omitempty"`
	ShellExec            commandList `json:"shell_exec,omitempty" yaml:"shell_exec,omitempty"`
	ShellLogin           commandList `json:"shell_login,omitempty" yaml:"shell_login,omitempty"`
}

func (t RuntimeCommandTemplates) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.wire())
}

func (t *RuntimeCommandTemplates) UnmarshalJSON(data []byte) error {
	var wire runtimeCommandTemplatesWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*t = wire.templates()
	return nil
}

func (t RuntimeCommandTemplates) MarshalYAML() (any, error) {
	return t.wire(), nil
}

func (t *RuntimeCommandTemplates) UnmarshalYAML(node *yaml.Node) error {
	var wire runtimeCommandTemplatesWire
	if err := node.Decode(&wire); err != nil {
		return err
	}

	*t = wire.templates()
	return nil
}

func (t RuntimeCommandTemplates) wire() runtimeCommandTemplatesWire {
	return runtimeCommandTemplatesWire{
		Version:              commandList(copyCommandList(t.Version)),
		List:                 commandList(copyCommandList(t.List)),
		Create:               commandList(copyCommandList(t.Create)),
		Start:                commandList(copyCommandList(t.Start)),
		Stop:                 commandList(copyCommandList(t.Stop)),
		Delete:               commandList(copyCommandList(t.Delete)),
		Clone:                commandList(copyCommandList(t.Clone)),
		Bootstrap:            commandList(copyCommandList(t.Bootstrap)),
		WorkspaceSeedPrepare: commandList(copyCommandList(t.WorkspaceSeedPrepare)),
		Copy:                 commandList(copyCommandList(t.Copy)),
		ShellExec:            commandList(copyCommandList(t.ShellExec)),
		ShellLogin:           commandList(copyCommandList(t.ShellLogin)),
	}
}

func (w runtimeCommandTemplatesWire) templates() RuntimeCommandTemplates {
	return RuntimeCommandTemplates{
		Version:              copyCommandList([]string(w.Version)),
		List:                 copyCommandList([]string(w.List)),
		Create:               copyCommandList([]string(w.Create)),
		Start:                copyCommandList([]string(w.Start)),
		Stop:                 copyCommandList([]string(w.Stop)),
		Delete:               copyCommandList([]string(w.Delete)),
		Clone:                copyCommandList([]string(w.Clone)),
		Bootstrap:            copyCommandList([]string(w.Bootstrap)),
		WorkspaceSeedPrepare: copyCommandList([]string(w.WorkspaceSeedPrepare)),
		Copy:                 copyCommandList([]string(w.Copy)),
		ShellExec:            copyCommandList([]string(w.ShellExec)),
		ShellLogin:           copyCommandList([]string(w.ShellLogin)),
	}
}

func commandSliceWithLegacy(primary, environmentCommands, setupCommands []string) []string {
	switch {
	case primary != nil:
		return append([]string(nil), primary...)
	case environmentCommands != nil:
		return append([]string(nil), environmentCommands...)
	case setupCommands != nil:
		return append([]string(nil), setupCommands...)
	default:
		return []string{}
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}

	return cloned
}
