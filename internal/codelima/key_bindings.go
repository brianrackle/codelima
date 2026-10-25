package codelima

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"go.rockorager.dev/vaxis"
)

// KeyBindingConfig contains authored overrides. Omitted actions inherit their
// defaults; an empty list explicitly disables an optional action.
type KeyBindingConfig map[string][]string

type keyActionDefinition struct {
	ID, Description     string
	Defaults, Contexts  []string
	Required, TextInput bool
}

// KeyBindingInfo describes one action in an effective, validated snapshot.
type KeyBindingInfo struct {
	Action      string   `json:"action" yaml:"action"`
	Description string   `json:"description" yaml:"description"`
	Defaults    []string `json:"defaults" yaml:"defaults"`
	Effective   []string `json:"effective" yaml:"effective"`
	Contexts    []string `json:"contexts" yaml:"contexts"`
	Required    bool     `json:"required" yaml:"required"`
}

// compiledKeyMap owns all its data and is immutable after compilation. Event
// dispatch and visible hints read the same snapshot without changing Key events.
type compiledKeyMap struct {
	contexts map[string]map[keyBindingIdentity]string
	actions  map[string][]keyBindingIdentity
	info     []KeyBindingInfo
}

func compileKeyBindings(overrides KeyBindingConfig, definitions []keyActionDefinition) (*compiledKeyMap, error) {
	m := &compiledKeyMap{contexts: make(map[string]map[keyBindingIdentity]string), actions: make(map[string][]keyBindingIdentity)}
	definitions = slices.Clone(definitions)
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	known := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition.ID == "" || known[definition.ID] || len(definition.Contexts) == 0 {
			return nil, invalidArgument("invalid key action definition: "+definition.ID, nil)
		}
		known[definition.ID] = true
	}
	names := make([]string, 0, len(overrides))
	for id := range overrides {
		names = append(names, id)
	}
	slices.Sort(names)
	for _, id := range names {
		if !known[id] {
			return nil, invalidArgument("keys."+id+": unknown action", nil)
		}
	}
	for _, definition := range definitions {
		values, overridden := overrides[definition.ID]
		if !overridden {
			values = definition.Defaults
		}
		bindings, err := compileActionBindings(definition, values)
		if err != nil {
			return nil, err
		}
		defaults, err := compileActionBindings(definition, definition.Defaults)
		if err != nil {
			return nil, err
		}
		contexts := slices.Clone(definition.Contexts)
		slices.Sort(contexts)
		contexts = slices.Compact(contexts)
		for _, context := range contexts {
			if m.contexts[context] == nil {
				m.contexts[context] = make(map[keyBindingIdentity]string)
			}
			for i, binding := range bindings {
				if other := m.contexts[context][binding]; other != "" {
					return nil, invalidArgument(fmt.Sprintf("keys.%s: %q conflicts with keys.%s in %s", definition.ID, values[i], other, context), nil)
				}
				m.contexts[context][binding] = definition.ID
			}
		}
		m.actions[definition.ID] = bindings
		m.info = append(m.info, KeyBindingInfo{Action: definition.ID, Description: definition.Description,
			Defaults: keyBindingSpellings(defaults), Effective: keyBindingSpellings(bindings), Contexts: contexts, Required: definition.Required})
	}
	return m, nil
}

func compileActionBindings(definition keyActionDefinition, values []string) ([]keyBindingIdentity, error) {
	setting := "keys." + definition.ID
	if len(values) == 0 && definition.Required {
		return nil, invalidArgument(setting+": required action must have a binding", nil)
	}
	if len(values) > 8 {
		return nil, invalidArgument(setting+": at most 8 bindings are allowed", nil)
	}
	bindings := make([]keyBindingIdentity, 0, len(values))
	for _, value := range values {
		binding, err := parseKeyBinding(value)
		if err != nil {
			return nil, invalidArgument(fmt.Sprintf("%s: %q: %s", setting, value, err), nil)
		}
		if slices.Contains(bindings, binding) {
			return nil, invalidArgument(fmt.Sprintf("%s: duplicate binding %q", setting, value), nil)
		}
		if definition.TextInput && binding.modifiers&^vaxis.ModShift == 0 && printableBindingRune(binding.code) {
			return nil, invalidArgument(fmt.Sprintf("%s: %q would consume text input; use a modified or nonprinting key", setting, value), nil)
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func keyBindingSpellings(bindings []keyBindingIdentity) []string {
	values := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		values = append(values, binding.spelling(false))
	}
	return values
}

func (m *compiledKeyMap) Action(context string, key vaxis.Key) string {
	if m == nil || key.EventType == vaxis.EventPaste {
		return ""
	}
	for _, identity := range keyBindingCandidates(key) {
		if action := m.contexts[context][identity]; action != "" {
			return action
		}
	}
	return ""
}

func (m *compiledKeyMap) Hint(action string) string {
	if m == nil || len(m.actions[action]) == 0 {
		return "unbound"
	}
	values := make([]string, 0, len(m.actions[action]))
	for _, binding := range m.actions[action] {
		values = append(values, binding.spelling(true))
	}
	return strings.Join(values, " / ")
}

func (m *compiledKeyMap) Describe() []KeyBindingInfo {
	if m == nil {
		return nil
	}
	info := slices.Clone(m.info)
	for i := range info {
		info[i].Defaults = slices.Clone(info[i].Defaults)
		info[i].Effective = slices.Clone(info[i].Effective)
		info[i].Contexts = slices.Clone(info[i].Contexts)
	}
	return info
}

func cloneKeyBindingConfig(config KeyBindingConfig) KeyBindingConfig {
	if config == nil {
		return nil
	}
	copyConfig := make(KeyBindingConfig, len(config))
	for action, bindings := range config {
		copyConfig[action] = slices.Clone(bindings)
	}
	return copyConfig
}
