package codelima

import (
	"encoding/json"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	RuntimeVM = "vm"

	ProviderLima = "lima"

	WorkspaceModeCopy    = "copy"
	WorkspaceModeMounted = "mounted"
	DefaultWorkspaceMode = WorkspaceModeMounted

	DefaultConfigurationSlug = "small"
	DefaultVCPUs             = uint8(2)
	DefaultMemoryMiB         = uint32(4 * 1024)
	DefaultDiskMiB           = uint32(25 * 1024)
)

// NodeStatus is the CodeLima-owned durable lifecycle state persisted in
// node.yaml. It is a distinct type from ObservationStatus: the two vocabularies
// overlap textually ("running") but mean different things — durable intent
// versus a transient runtime sighting — and must not be compared directly.
type NodeStatus string

const (
	NodeStatusCreated      NodeStatus = "created"
	NodeStatusProvisioning NodeStatus = "provisioning"
	NodeStatusRegistering  NodeStatus = "registering"
	NodeStatusRunning      NodeStatus = "running"
	NodeStatusStopped      NodeStatus = "stopped"
	NodeStatusFailed       NodeStatus = "failed"
	NodeStatusTerminating  NodeStatus = "terminating"
	NodeStatusTerminated   NodeStatus = "terminated"
)

// ObservationStatus is what the sandbox runtime reported for an instance the
// last time it was listed. It is never persisted as node lifecycle.
type ObservationStatus string

const (
	ObservationUninitialized ObservationStatus = "uninitialized"
	ObservationInstalling    ObservationStatus = "installing"
	ObservationBroken        ObservationStatus = "broken"
	ObservationRunning       ObservationStatus = "running"
	ObservationStopped       ObservationStatus = "stopped"
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
	Name          string            `json:"name,omitempty" yaml:"name,omitempty"`
	Exists        bool              `json:"exists" yaml:"exists"`
	Status        ObservationStatus `json:"status,omitempty" yaml:"status,omitempty"`
	Dir           string            `json:"dir,omitempty" yaml:"dir,omitempty"`
	Hostname      string            `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	SSHAddress    string            `json:"ssh_address,omitempty" yaml:"ssh_address,omitempty"`
	SSHPort       int               `json:"ssh_port,omitempty" yaml:"ssh_port,omitempty"`
	SSHConfigFile string            `json:"ssh_config_file,omitempty" yaml:"ssh_config_file,omitempty"`
	LimaHome      string            `json:"lima_home,omitempty" yaml:"lima_home,omitempty"`
	LimaVersion   string            `json:"lima_version,omitempty" yaml:"lima_version,omitempty"`
	VMType        string            `json:"vm_type,omitempty" yaml:"vm_type,omitempty"`
	// CPUUsagePercent is the aggregate busy percentage across the node's
	// vCPUs, normalized to 0..100 for the sampling interval. It is transient
	// runtime telemetry and is never persisted in node.yaml.
	CPUUsagePercent   *float64   `json:"cpu_usage_percent,omitempty" yaml:"cpu_usage_percent,omitempty"`
	CPUUsageSampledAt *time.Time `json:"cpu_usage_sampled_at,omitempty" yaml:"cpu_usage_sampled_at,omitempty"`
	// Memory and disk usage are transient guest observations. Memory uses
	// MemTotal - MemAvailable; disk is the guest root filesystem reported by
	// df, excluding the separately mounted host workspace.
	MemoryUsedBytes        *uint64    `json:"memory_used_bytes,omitempty" yaml:"memory_used_bytes,omitempty"`
	MemoryTotalBytes       *uint64    `json:"memory_total_bytes,omitempty" yaml:"memory_total_bytes,omitempty"`
	DiskUsedBytes          *uint64    `json:"disk_used_bytes,omitempty" yaml:"disk_used_bytes,omitempty"`
	DiskTotalBytes         *uint64    `json:"disk_total_bytes,omitempty" yaml:"disk_total_bytes,omitempty"`
	ResourceUsageSampledAt *time.Time `json:"resource_usage_sampled_at,omitempty" yaml:"resource_usage_sampled_at,omitempty"`
}

type Node struct {
	ID                     string                  `json:"id" yaml:"id"`
	Slug                   string                  `json:"slug" yaml:"slug"`
	ConfigurationID        string                  `json:"configuration_id" yaml:"configuration_id"`
	ConfigurationSlug      string                  `json:"configuration_slug,omitempty" yaml:"configuration_slug,omitempty"`
	DirectoryPath          string                  `json:"directory_path" yaml:"directory_path"`
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
	Status                 NodeStatus              `json:"status" yaml:"status"`
	LifecycleState         NodeStatus              `json:"-" yaml:"-"`
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
	LifecycleState       NodeStatus              `json:"lifecycle_state,omitempty" yaml:"lifecycle_state,omitempty"`
	Status               NodeStatus              `json:"status,omitempty" yaml:"status,omitempty"`
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

func normalizeNodeLifecycleState(state NodeStatus) NodeStatus {
	switch NodeStatus(strings.TrimSpace(strings.ToLower(string(state)))) {
	case NodeStatusCreated,
		NodeStatusProvisioning,
		NodeStatusRegistering,
		NodeStatusFailed,
		NodeStatusTerminating,
		NodeStatusTerminated:
		return NodeStatus(strings.TrimSpace(strings.ToLower(string(state))))
	default:
		return ""
	}
}

func legacyNodeStatus(state NodeStatus) NodeStatus {
	switch NodeStatus(strings.TrimSpace(strings.ToLower(string(state)))) {
	case NodeStatusCreated,
		NodeStatusProvisioning,
		NodeStatusRegistering,
		NodeStatusRunning,
		NodeStatusStopped,
		NodeStatusFailed,
		NodeStatusTerminating,
		NodeStatusTerminated:
		return NodeStatus(strings.TrimSpace(strings.ToLower(string(state))))
	default:
		return ""
	}
}

func nodeLifecycleState(node Node) NodeStatus {
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
		Status:               status,
		LifecycleState:       lifecycleState,
		AgentProfileName:     w.AgentProfileName,
		RuntimeCommands:      w.RuntimeCommands,
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

// cloneStringMap copies a string map, returning an empty map rather than nil
// for an absent source so callers can always write into the result. It is not
// maps.Clone for exactly that reason: maps.Clone(nil) is nil.
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
