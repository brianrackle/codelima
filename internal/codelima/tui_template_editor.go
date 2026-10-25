package codelima

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The editor is shared by Templates, Spec and the create-node picker. Values
// stay on the event loop; workers return snapshots addressed to this instance.
type tuiTemplateEditor struct {
	tuiOverlayKeys
	app                                             *vaxisTUIApp
	parent                                          tuiOverlay
	dialog                                          *tuiDialog
	original                                        Configuration
	creating, initialized, loading, loaded, pending bool
	data                                            tuiTemplateEditorDataEvent
	onSave                                          func(Configuration)
	environment                                     string
	scroll, previewScroll                           int
	preview                                         bool
	notice                                          string
	controls                                        []tuiTemplateControl
}

type tuiTemplateControl struct {
	rect        tuiRect
	field, step int
	environment string
}
type tuiTemplateEditorDataEvent struct {
	editor       *tuiTemplateEditor
	templates    []Configuration
	environments []EnvironmentConfig
	profiles     []AgentProfile
	nodes        []Node
	err          error
}
type tuiTemplateSavedEvent struct {
	editor   *tuiTemplateEditor
	template Configuration
	count    int
	err      error
}

func (a *vaxisTUIApp) openTemplateEditor(c Configuration, creating bool, parent tuiOverlay, onSave func(Configuration)) *tuiTemplateEditor {
	e := &tuiTemplateEditor{app: a, parent: parent, original: cloneConfiguration(c), creating: creating, initialized: c.Image != "", onSave: onSave}
	if creating {
		e.original.ID, e.original.Slug = "", ""
	}
	e.dialog = newTUIDialog("Template", "Save", nil, []tuiDialogField{
		newTUIInputField("slug", "Name", e.original.Slug, true),
		newTUIInputField("image", "Image", c.Image, true),
		newTUIInputField("agent", "Agent profile", c.AgentProfileName, true),
		newTUIInputField("vcpus", "vCPUs", strconv.Itoa(int(c.VCPUs)), true),
		newTUIInputField("memory", "Memory", formatTemplateSize(c.MemoryMiB), true),
		newTUIInputField("disk", "Disk", formatTemplateSize(c.DiskMiB), true),
		newTUISelectorField("environments", "Environments", strings.Join(c.Environments, ","), false, nil),
		newTUISelectorField("bootstrap", "Bootstrap commands", "", false, nil),
	}, nil)
	e.setCommands(c.BootstrapCommands)
	e.dialog.initialValues["bootstrap"] = e.value("bootstrap")
	a.overlay = e
	e.load()
	return e
}

func (e *tuiTemplateEditor) ParentOverlay() tuiOverlay { return e.parent }
func (e *tuiTemplateEditor) FullScreen() bool          { return true }
func (e *tuiTemplateEditor) value(key string) string {
	for _, f := range e.dialog.Fields {
		if f.Key == key {
			return f.rawValue()
		}
	}
	return ""
}
func (e *tuiTemplateEditor) visible() bool {
	for o := e.app.overlay; o != nil; o = overlayParent(o) {
		if o == e {
			return true
		}
	}
	return false
}
func (a *vaxisTUIApp) visibleTemplateEditor() *tuiTemplateEditor {
	for o := a.overlay; o != nil; o = overlayParent(o) {
		if e, ok := o.(*tuiTemplateEditor); ok {
			return e
		}
	}
	return nil
}
func (e *tuiTemplateEditor) workContext() (context.Context, context.CancelFunc) {
	ctx := e.app.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, 10*time.Second)
}
func (e *tuiTemplateEditor) load() {
	if e.loading {
		return
	}
	if e.app.service == nil {
		e.data.err = fmt.Errorf("Template service unavailable")
		return
	}
	e.loading = true
	service := e.app.service
	ctx, cancel := e.workContext()
	load := func() tuiTemplateEditorDataEvent {
		defer cancel()
		d := tuiTemplateEditorDataEvent{editor: e}
		d.templates, d.err = service.ConfigurationList(ctx, false)
		if d.err == nil {
			d.environments, d.err = service.EnvironmentConfigList(ctx, false)
		}
		if d.err == nil {
			d.nodes, d.err = service.store.ListNodes(false)
		}
		if d.err == nil {
			var entries []os.DirEntry
			entries, d.err = os.ReadDir(service.cfg.AgentProfilesDir)
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
					continue
				}
				profile, err := service.store.LoadAgentProfile(strings.TrimSuffix(entry.Name(), ".yaml"))
				if err == nil {
					d.profiles = append(d.profiles, profile)
				}
			}
		}
		return d
	}
	if e.app.postEvent == nil {
		e.app.applyTemplateEditorData(load())
		return
	}
	go func() { e.app.postCompletion(load()) }()
}
func (a *vaxisTUIApp) applyTemplateEditorData(d tuiTemplateEditorDataEvent) {
	e := d.editor
	if !e.visible() {
		return
	}
	e.loading = false
	e.data = d
	if d.err != nil {
		return
	}
	e.loaded = true
	if !e.initialized {
		for _, c := range d.templates {
			if c.Slug == DefaultConfigurationSlug {
				e.original = cloneConfiguration(c)
				e.original.ID, e.original.Slug = "", ""
				e.applyValues(e.original)
				for _, f := range e.dialog.Fields {
					if f.Key != "slug" {
						e.dialog.initialValues[f.Key] = f.rawValue()
					}
				}
				e.initialized = true
				break
			}
		}
	}
}
func (e *tuiTemplateEditor) applyValues(c Configuration) {
	for k, v := range map[string]string{"image": c.Image, "agent": c.AgentProfileName, "vcpus": strconv.Itoa(int(c.VCPUs)), "memory": formatTemplateSize(c.MemoryMiB), "disk": formatTemplateSize(c.DiskMiB)} {
		e.dialog.SetFieldValue(k, v)
	}
	e.setEnvironments(c.Environments)
	e.setCommands(c.BootstrapCommands)
}
func (e *tuiTemplateEditor) setEnvironments(names []string) {
	e.dialog.SetFieldValue("environments", strings.Join(names, ","))
}
func (e *tuiTemplateEditor) environments() []string {
	return parseCommaSeparatedValues(e.value("environments"))
}
func (e *tuiTemplateEditor) setCommands(commands []string) {
	if commands == nil {
		commands = []string{}
	}
	raw, _ := json.Marshal(commands)
	e.dialog.SetFieldValue("bootstrap", string(raw))
}
func (e *tuiTemplateEditor) commands() []string {
	commands := []string{}
	_ = json.Unmarshal([]byte(e.value("bootstrap")), &commands)
	return commands
}
func (e *tuiTemplateEditor) environmentRows() []EnvironmentConfig {
	var rows []EnvironmentConfig
	for _, name := range e.environments() {
		found := false
		for _, env := range e.data.environments {
			if env.Slug == name {
				rows = append(rows, env)
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, EnvironmentConfig{Slug: name})
		}
	}
	for _, env := range e.data.environments {
		if !slices.Contains(e.environments(), env.Slug) {
			rows = append(rows, env)
		}
	}
	if !slices.ContainsFunc(rows, func(env EnvironmentConfig) bool { return env.Slug == e.environment }) && len(rows) > 0 {
		e.environment = rows[0].Slug
	}
	return rows
}
func (e *tuiTemplateEditor) toggleEnvironment(name string) {
	names := e.environments()
	i := slices.Index(names, name)
	if i >= 0 {
		names = slices.Delete(names, i, i+1)
	} else {
		names = append(names, name)
	}
	e.setEnvironments(names)
	e.notice = ""
}
func (e *tuiTemplateEditor) moveEnvironment(name string, delta int) {
	names := e.environments()
	i := slices.Index(names, name)
	j := i + delta
	if i >= 0 && j >= 0 && j < len(names) {
		names[i], names[j] = names[j], names[i]
		e.setEnvironments(names)
		e.notice = name + " moved in execution order"
	}
}
func parseTemplateSize(value string) (uint32, error) {
	s := strings.TrimSpace(strings.ToLower(value))
	if strings.HasSuffix(s, "g") || strings.HasSuffix(s, "m") {
		s += "ib"
	}
	return parseSizeMiB(s)
}
func formatTemplateSize(value uint32) string {
	if value > 0 && value%1024 == 0 {
		return fmt.Sprintf("%d GiB", value/1024)
	}
	return fmt.Sprintf("%d MiB", value)
}
func (e *tuiTemplateEditor) stepResource(index, delta int) {
	key := e.dialog.Fields[index].Key
	value := uint64(0)
	steps := []uint64{1, 2, 4, 6, 8, 12, 16, 24, 32, 48, 64, 96, 128, 192, 255}
	if index == 3 {
		value, _ = strconv.ParseUint(e.value(key), 10, 8)
	} else {
		v, _ := parseTemplateSize(e.value(key))
		value = uint64(v)
		steps = []uint64{512, 1024, 2048, 4096, 8192, 12288, 16384, 24576, 32768, 49152, 65536, 98304, 131072, 262144}
		if index == 5 {
			steps = []uint64{1024, 5120, 10240, 20480, 32768, 51200, 76800, 102400, 204800, 512000, 1048576}
		}
	}
	next := value
	if delta > 0 {
		for _, v := range steps {
			if v > value {
				next = v
				break
			}
		}
	} else {
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i] < value {
				next = steps[i]
				break
			}
		}
	}
	text := strconv.FormatUint(next, 10)
	if index != 3 {
		text = formatTemplateSize(uint32(next))
	}
	e.dialog.SetFieldValue(key, text)
}
func (e *tuiTemplateEditor) draft() (Configuration, error) {
	if e.data.err != nil {
		return Configuration{}, e.data.err
	}
	if !e.loaded || !e.initialized {
		return Configuration{}, fmt.Errorf("loading template definitions…")
	}
	c := cloneConfiguration(e.original)
	c.Slug, c.Image, c.AgentProfileName = e.value("slug"), e.value("image"), e.value("agent")
	if c.Slug == "" || slugify(c.Slug) != c.Slug || c.Slug == legacyDefaultConfigurationSlug {
		return c, fmt.Errorf("Name must be a unique lowercase slug")
	}
	if !e.creating && e.original.Slug == DefaultConfigurationSlug && c.Slug != DefaultConfigurationSlug {
		return c, fmt.Errorf("small is the default and cannot be renamed")
	}
	for _, other := range e.data.templates {
		if c.Slug == other.Slug && (e.creating || other.ID != c.ID) {
			return c, fmt.Errorf("Name already used: %s", c.Slug)
		}
	}
	if c.Image == "" {
		return c, fmt.Errorf("Image is required")
	}
	if !slices.ContainsFunc(e.data.profiles, func(p AgentProfile) bool { return p.Name == c.AgentProfileName }) {
		return c, fmt.Errorf("Choose an available agent profile")
	}
	cpu, err := strconv.ParseUint(e.value("vcpus"), 10, 8)
	if err != nil || cpu == 0 {
		return c, fmt.Errorf("vCPUs must be from 1 to 255")
	}
	c.VCPUs = uint8(cpu)
	if c.MemoryMiB, err = parseTemplateSize(e.value("memory")); err != nil {
		return c, fmt.Errorf("Memory must be a positive size, for example 20G")
	}
	if c.DiskMiB, err = parseTemplateSize(e.value("disk")); err != nil {
		return c, fmt.Errorf("Disk must be a positive size, for example 75G")
	}
	c.Environments = e.environments()
	if c.Environments == nil {
		c.Environments = []string{}
	}
	c.BootstrapCommands = e.commands()
	for _, name := range c.Environments {
		if !slices.ContainsFunc(e.data.environments, func(env EnvironmentConfig) bool { return env.Slug == name }) {
			return c, fmt.Errorf("Environment unavailable: %s", name)
		}
	}
	return c, nil
}
func (e *tuiTemplateEditor) previewSteps() ([]BootstrapStep, error) {
	c := cloneConfiguration(e.original)
	c.Slug = e.value("slug")
	c.Environments = e.environments()
	c.BootstrapCommands = e.commands()
	var profile AgentProfile
	for _, p := range e.data.profiles {
		if p.Name == e.value("agent") {
			profile = p
			break
		}
	}
	if profile.Name == "" {
		return nil, fmt.Errorf("Agent profile unavailable")
	}
	steps, err := configurationBootstrapSteps(c, func(name string) (EnvironmentConfig, error) {
		for _, env := range e.data.environments {
			if env.Slug == name || env.ID == name {
				return env, nil
			}
		}
		return EnvironmentConfig{}, fmt.Errorf("Environment unavailable: %s", name)
	})
	if err != nil {
		return nil, err
	}
	prefix := appendBootstrapSteps(nil, profile.InstallCommands, "bootstrap", "agent", "", profile.Name)
	prefix = append(prefix, steps...)
	return appendBootstrapSteps(prefix, []string{profile.ValidationCommand}, "validation", "agent", "", profile.Name), nil
}
func (e *tuiTemplateEditor) save() {
	c, err := e.draft()
	if err != nil {
		e.dialog.Error = err.Error()
		return
	}
	e.pending = true
	e.dialog.Error = ""
	ctx, cancel := e.workContext()
	service := e.app.service
	creating := e.creating
	save := func() tuiTemplateSavedEvent {
		defer cancel()
		event := tuiTemplateSavedEvent{editor: e, count: -1}
		input := ConfigurationCreateInput{Slug: c.Slug, Image: &c.Image, AgentProfile: &c.AgentProfileName, VCPUs: &c.VCPUs, MemoryMiB: &c.MemoryMiB, DiskMiB: &c.DiskMiB, Environments: c.Environments, BootstrapCommands: c.BootstrapCommands}
		if creating {
			event.template, event.err = service.ConfigurationCreate(ctx, input)
		} else {
			event.template, event.err = service.ConfigurationUpdate(ctx, c.ID, input)
		}
		if event.err == nil {
			if nodes, err := service.store.ConfigurationNodes(event.template.ID, false); err == nil {
				event.count = len(nodes)
			}
		}
		return event
	}
	if e.app.postEvent == nil {
		e.app.applyTemplateSaved(save())
		return
	}
	go func() { e.app.postCompletion(save()) }()
}
func (a *vaxisTUIApp) applyTemplateSaved(event tuiTemplateSavedEvent) {
	e := event.editor
	e.pending = false
	if !e.visible() {
		return
	}
	if event.err != nil {
		e.dialog.Error = event.err.Error()
		e.load()
		return
	}
	a.overlay = e.parent
	if e.onSave != nil {
		e.onSave(event.template)
	}
	message := "Saved template " + event.template.Slug + " · existing nodes keep their frozen values"
	if event.count >= 0 {
		message = fmt.Sprintf("Saved template %s · %d existing node(s) keep their frozen values", event.template.Slug, event.count)
	}
	a.setStatus(slog.LevelInfo, message)
	a.templateSelectID = event.template.ID
	a.loadManagerData(false)
}
func builtInTemplateDefaults(slug string, cfg Config) (Configuration, bool) {
	for _, s := range builtInConfigurationSpecs() {
		if s.Slug == slug {
			return Configuration{Slug: slug, Image: cfg.DefaultImage, AgentProfileName: cfg.DefaultAgentProfile, VCPUs: s.VCPUs, MemoryMiB: s.MemoryMiB, DiskMiB: s.DiskMiB, Environments: defaultConfigurationEnvironmentSlugs(), BootstrapCommands: []string{}}, true
		}
	}
	return Configuration{}, false
}
func (e *tuiTemplateEditor) restoreDefaults() {
	if c, ok := builtInTemplateDefaults(e.original.Slug, e.app.managerSettings()); ok && !e.creating {
		e.applyValues(c)
		e.notice = e.bindingHints("Built-in defaults loaded · {dialog.submit} saves them")
	}
}
