package codelima

import "time"

const (
	RuntimeVM        = "vm"
	RuntimeContainer = "container"

	ProviderLima   = "lima"
	ProviderColima = "colima"

	NodeStatusCreated      = "created"
	NodeStatusProvisioning = "provisioning"
	NodeStatusRegistering  = "registering"
	NodeStatusRunning      = "running"
	NodeStatusStopped      = "stopped"
	NodeStatusFailed       = "failed"
	NodeStatusTerminating  = "terminating"
	NodeStatusTerminated   = "terminated"

	PatchDirectionChildToParent = "child_to_parent"
	PatchDirectionParentToChild = "parent_to_child"

	PatchStatusDraft     = "draft"
	PatchStatusSubmitted = "submitted"
	PatchStatusApproved  = "approved"
	PatchStatusApplied   = "applied"
	PatchStatusRejected  = "rejected"
	PatchStatusFailed    = "failed"
)

type Resources struct {
	CPUs      int `json:"cpus" yaml:"cpus"`
	MemoryGiB int `json:"memory_gib" yaml:"memory_gib"`
	DiskGiB   int `json:"disk_gib" yaml:"disk_gib"`
}

func (r Resources) ApplyDefaults(defaults Resources) Resources {
	if r.CPUs == 0 {
		r.CPUs = defaults.CPUs
	}

	if r.MemoryGiB == 0 {
		r.MemoryGiB = defaults.MemoryGiB
	}

	if r.DiskGiB == 0 {
		r.DiskGiB = defaults.DiskGiB
	}

	return r
}

type Project struct {
	ID                  string     `json:"id" yaml:"id"`
	Slug                string     `json:"slug" yaml:"slug"`
	WorkspacePath       string     `json:"workspace_path" yaml:"workspace_path"`
	ParentProjectID     string     `json:"parent_project_id,omitempty" yaml:"parent_project_id,omitempty"`
	ForkBaseSnapshotID  string     `json:"fork_base_snapshot_id,omitempty" yaml:"fork_base_snapshot_id,omitempty"`
	AgentProfileName    string     `json:"agent_profile_name" yaml:"agent_profile_name"`
	SetupCommands       []string   `json:"setup_commands" yaml:"setup_commands"`
	DefaultRuntime      string     `json:"default_runtime" yaml:"default_runtime"`
	DefaultProvider     string     `json:"default_provider" yaml:"default_provider"`
	DefaultLimaTemplate string     `json:"default_lima_template" yaml:"default_lima_template"`
	DefaultResources    Resources  `json:"default_resources" yaml:"default_resources"`
	CreatedAt           time.Time  `json:"created_at" yaml:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at" yaml:"updated_at"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
}

type RuntimeObservation struct {
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	Exists   bool   `json:"exists" yaml:"exists"`
	Status   string `json:"status,omitempty" yaml:"status,omitempty"`
	Dir      string `json:"dir,omitempty" yaml:"dir,omitempty"`
	Hostname string `json:"hostname,omitempty" yaml:"hostname,omitempty"`
}

type Node struct {
	ID                     string              `json:"id" yaml:"id"`
	Slug                   string              `json:"slug" yaml:"slug"`
	ProjectID              string              `json:"project_id" yaml:"project_id"`
	ParentNodeID           string              `json:"parent_node_id,omitempty" yaml:"parent_node_id,omitempty"`
	Runtime                string              `json:"runtime" yaml:"runtime"`
	Provider               string              `json:"provider" yaml:"provider"`
	LimaInstanceName       string              `json:"lima_instance_name" yaml:"lima_instance_name"`
	RequestedResources     Resources           `json:"requested_resources" yaml:"requested_resources"`
	Status                 string              `json:"status" yaml:"status"`
	AgentProfileName       string              `json:"agent_profile_name" yaml:"agent_profile_name"`
	BootstrapCommands      []string            `json:"bootstrap_commands" yaml:"bootstrap_commands"`
	GeneratedTemplatePath  string              `json:"generated_template_path" yaml:"generated_template_path"`
	GuestWorkspacePath     string              `json:"guest_workspace_path,omitempty" yaml:"guest_workspace_path,omitempty"`
	WorkspaceMountPath     string              `json:"workspace_mount_path,omitempty" yaml:"workspace_mount_path,omitempty"`
	WorkspaceSeeded        bool                `json:"workspace_seeded" yaml:"workspace_seeded"`
	BootstrapCompleted     bool                `json:"bootstrap_completed" yaml:"bootstrap_completed"`
	BootstrapCompletedAt   *time.Time          `json:"bootstrap_completed_at,omitempty" yaml:"bootstrap_completed_at,omitempty"`
	CreatedAt              time.Time           `json:"created_at" yaml:"created_at"`
	UpdatedAt              time.Time           `json:"updated_at" yaml:"updated_at"`
	DeletedAt              *time.Time          `json:"deleted_at,omitempty" yaml:"deleted_at,omitempty"`
	LastReconciledAt       *time.Time          `json:"last_reconciled_at,omitempty" yaml:"last_reconciled_at,omitempty"`
	LastRuntimeObservation *RuntimeObservation `json:"last_runtime_observation,omitempty" yaml:"last_runtime_observation,omitempty"`
}

type BootstrapState struct {
	AgentProfileName  string            `json:"agent_profile_name" yaml:"agent_profile_name"`
	InstallCommands   []string          `json:"install_commands" yaml:"install_commands"`
	SetupCommands     []string          `json:"setup_commands" yaml:"setup_commands"`
	ValidationCommand string            `json:"validation_command" yaml:"validation_command"`
	LaunchCommand     string            `json:"launch_command" yaml:"launch_command"`
	Environment       map[string]string `json:"environment" yaml:"environment"`
	Completed         bool              `json:"completed" yaml:"completed"`
	CompletedAt       *time.Time        `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
}

func (b BootstrapState) CombinedCommands() []string {
	commands := make([]string, 0, len(b.InstallCommands)+len(b.SetupCommands))
	commands = append(commands, b.InstallCommands...)
	commands = append(commands, b.SetupCommands...)
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

type ApprovalMetadata struct {
	Actor     string    `json:"actor" yaml:"actor"`
	Timestamp time.Time `json:"timestamp" yaml:"timestamp"`
	Note      string    `json:"note,omitempty" yaml:"note,omitempty"`
}

type DiffSummary struct {
	FilesChanged  int      `json:"files_changed" yaml:"files_changed"`
	AddedFiles    int      `json:"added_files" yaml:"added_files"`
	ModifiedFiles int      `json:"modified_files" yaml:"modified_files"`
	DeletedFiles  int      `json:"deleted_files" yaml:"deleted_files"`
	Paths         []string `json:"paths" yaml:"paths"`
}

type ConflictSummary struct {
	Message string `json:"message" yaml:"message"`
	Details string `json:"details,omitempty" yaml:"details,omitempty"`
}

type ApplyResult struct {
	AppliedAt      time.Time `json:"applied_at" yaml:"applied_at"`
	PostSnapshotID string    `json:"post_snapshot_id" yaml:"post_snapshot_id"`
	RecoveryNote   string    `json:"recovery_note,omitempty" yaml:"recovery_note,omitempty"`
}

type PatchProposal struct {
	ID               string            `json:"id" yaml:"id"`
	Direction        string            `json:"direction" yaml:"direction"`
	SourceProjectID  string            `json:"source_project_id" yaml:"source_project_id"`
	SourceNodeID     string            `json:"source_node_id,omitempty" yaml:"source_node_id,omitempty"`
	TargetProjectID  string            `json:"target_project_id" yaml:"target_project_id"`
	TargetNodeID     string            `json:"target_node_id,omitempty" yaml:"target_node_id,omitempty"`
	BaseSnapshotID   string            `json:"base_snapshot_id" yaml:"base_snapshot_id"`
	SourceSnapshotID string            `json:"source_snapshot_id" yaml:"source_snapshot_id"`
	TargetSnapshotID string            `json:"target_snapshot_id" yaml:"target_snapshot_id"`
	Status           string            `json:"status" yaml:"status"`
	PatchPath        string            `json:"patch_path" yaml:"patch_path"`
	DiffSummary      DiffSummary       `json:"diff_summary" yaml:"diff_summary"`
	ConflictSummary  *ConflictSummary  `json:"conflict_summary,omitempty" yaml:"conflict_summary,omitempty"`
	Approval         *ApprovalMetadata `json:"approval,omitempty" yaml:"approval,omitempty"`
	ApplyResult      *ApplyResult      `json:"apply_result,omitempty" yaml:"apply_result,omitempty"`
	CreatedAt        time.Time         `json:"created_at" yaml:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at" yaml:"updated_at"`
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

type ProjectTreeNode struct {
	Project  Project           `json:"project"`
	Children []ProjectTreeNode `json:"children"`
}
