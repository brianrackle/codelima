package codelima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

const nodeSpecYAMLLimit = 2 << 20

type nodeSpecField struct{ Name, Value string }
type nodeSpecDifference struct{ Name, Frozen, Current string }

type nodeSpecSnapshot struct {
	Node           Node
	Bootstrap      BootstrapState
	BootstrapError error
	Template       *Configuration
	TemplateError  error
	Differences    []nodeSpecDifference
	LimaPath       string
	LimaFields     []nodeSpecField
	LimaError      error
}

// NodeSpec reads the frozen recipe and today's template from local metadata.
// Inspection must remain available when Lima or a referenced recipe is broken.
// It never initializes storage, reconciles node state, or contacts the runtime.
func (s *Service) NodeSpec(ctx context.Context, value string) (nodeSpecSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nodeSpecSnapshot{}, err
	}
	node, err := s.store.NodeByIDOrSlug(value)
	if err != nil {
		return nodeSpecSnapshot{}, err
	}
	result := nodeSpecSnapshot{Node: node, LimaPath: filepath.Join(s.store.nodeDir(node.ID), "instance.lima.yaml")}
	result.Bootstrap, result.BootstrapError = s.store.LoadBootstrapState(node.ID)
	configuration, err := s.store.ConfigurationByID(node.ConfigurationID)
	if err == nil && configuration.DeletedAt != nil {
		err = errors.New("the source template was deleted")
	}
	if err != nil {
		result.TemplateError = err
	} else {
		result.Template = &configuration
		result.Node.ConfigurationSlug = configuration.Slug
		result.compareTemplate(configuration)
		commands, resolveErr := s.resolveConfigurationBootstrapCommands(configuration)
		if resolveErr == nil {
			var profile AgentProfile
			profile, resolveErr = s.store.LoadAgentProfile(configuration.AgentProfileName)
			if resolveErr == nil {
				current := append(slices.Clone(profile.InstallCommands), commands...)
				if !slices.Equal(node.BootstrapCommands, current) {
					result.Differences = append(result.Differences, nodeSpecDifference{"bootstrap", commandCount(node.BootstrapCommands), commandCount(current) + " · changed"})
				}
				if result.BootstrapError == nil {
					result.addDifference("validation", result.Bootstrap.ValidationCommand, profile.ValidationCommand)
					result.addDifference("launch", result.Bootstrap.LaunchCommand, profile.LaunchCommand)
					if !maps.Equal(result.Bootstrap.Environment, profile.Environment) {
						// Environment values may contain credentials; reveal names only.
						result.Differences = append(result.Differences, nodeSpecDifference{"agent environment", environmentKeys(result.Bootstrap.Environment), environmentKeys(profile.Environment) + " · changed"})
					}
				}
			}
		}
		result.TemplateError = resolveErr
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.LimaFields, result.LimaError = readNodeSpecLima(result.LimaPath, node.SandboxName)
	return result, ctx.Err()
}

func commandCount(commands []string) string { return fmt.Sprintf("%d commands", len(commands)) }

func environmentKeys(env map[string]string) string {
	return coalesce(strings.Join(slices.Sorted(maps.Keys(env)), ", "), "none")
}

func (s *nodeSpecSnapshot) addDifference(name, frozen, current string) {
	if frozen != current {
		s.Differences = append(s.Differences, nodeSpecDifference{name, frozen, current})
	}
}

func (s *nodeSpecSnapshot) compareTemplate(c Configuration) {
	n := s.Node
	s.addDifference("image", n.Image, c.Image)
	s.addDifference("vCPUs", strconv.Itoa(int(n.VCPUs)), strconv.Itoa(int(c.VCPUs)))
	s.addDifference("memory", formatTUIResourceSize(n.MemoryMiB), formatTUIResourceSize(c.MemoryMiB))
	s.addDifference("disk", formatTUIResourceSize(n.DiskMiB), formatTUIResourceSize(c.DiskMiB))
	s.addDifference("agent profile", n.AgentProfileName, c.AgentProfileName)
	s.addDifference("environments", strings.Join(n.Environments, " → "), strings.Join(c.Environments, " → "))
}

// Read the actual rendered YAML, with strict file and allocation bounds. Never
// infer host-specific settings from the machine running this inspection.
func readNodeSpecLima(path, name string) ([]nodeSpecField, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("Lima YAML must be a regular file")
	}
	if info.Size() > nodeSpecYAMLLimit {
		return nil, errors.New("Lima YAML exceeds the 2 MiB inspection limit; use v to view the file")
	}
	data, err := io.ReadAll(io.LimitReader(file, nodeSpecYAMLLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > nodeSpecYAMLLimit {
		return nil, errors.New("Lima YAML grew beyond the 2 MiB inspection limit")
	}
	var config struct {
		VMType     string                       `yaml:"vmType"`
		MountType  string                       `yaml:"mountType"`
		Nested     *bool                        `yaml:"nestedVirtualization"`
		Containerd struct{ System, User *bool } `yaml:"containerd"`
		Mounts     []struct {
			Location   string
			MountPoint string `yaml:"mountPoint"`
			Writable   *bool
		} `yaml:"mounts"`
		PortForwards []struct {
			GuestPort         int    `yaml:"guestPort"`
			HostPort          int    `yaml:"hostPort"`
			GuestPortRange    []int  `yaml:"guestPortRange"`
			GuestIP           string `yaml:"guestIP"`
			GuestIPMustBeZero *bool  `yaml:"guestIPMustBeZero"`
			Proto             string
			Ignore            bool
		} `yaml:"portForwards"`
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("read Lima YAML: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("Lima YAML must contain a mapping")
	}
	if err := document.Decode(&config); err != nil {
		return nil, fmt.Errorf("read Lima YAML: %w", err)
	}
	fields := []nodeSpecField{{"name", name}, {"vmType", coalesce(config.VMType, "not specified")}, {"mountType", coalesce(config.MountType, "not specified")}, {"nestedVirtualization", specBool(config.Nested)}}
	if len(config.Mounts) == 0 {
		fields = append(fields, nodeSpecField{"mounts", "none"})
	}
	for _, mount := range config.Mounts {
		mode := "read-only"
		if mount.Writable != nil && *mount.Writable {
			mode = "writable"
		}
		fields = append(fields, nodeSpecField{"mounts", mount.Location + " → " + coalesce(mount.MountPoint, "same path") + " · " + mode})
	}
	if len(config.PortForwards) == 0 {
		fields = append(fields, nodeSpecField{"portForwards", "not specified"})
	}
	for _, port := range config.PortForwards {
		value := fmt.Sprintf("%d:%d %s", port.HostPort, port.GuestPort, coalesce(port.Proto, "tcp"))
		if port.Ignore {
			value = fmt.Sprintf("ignore %d %s", port.GuestPort, coalesce(port.Proto, "tcp"))
			if len(port.GuestPortRange) == 2 {
				value = fmt.Sprintf("ignore %d–%d %s", port.GuestPortRange[0], port.GuestPortRange[1], coalesce(port.Proto, "tcp"))
				if port.GuestPortRange[0] == 1 && port.GuestPortRange[1] == 65535 && port.Proto == "any" && port.GuestIP == "0.0.0.0" && port.GuestIPMustBeZero != nil && !*port.GuestIPMustBeZero {
					value = "ignore all"
				}
			}
		}
		fields = append(fields, nodeSpecField{"portForwards", value})
	}
	containerd := "system=" + specBool(config.Containerd.System) + " user=" + specBool(config.Containerd.User)
	if config.Containerd.System != nil && config.Containerd.User != nil && !*config.Containerd.System && !*config.Containerd.User {
		containerd = "off"
	}
	fields = append(fields, nodeSpecField{"containerd", containerd})
	return fields, nil
}

func specBool(value *bool) string {
	if value == nil {
		return "not specified"
	}
	return strconv.FormatBool(*value)
}
