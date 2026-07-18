package codelima

import (
	"strings"
)

// These renderers exist only for the service fake's compatibility assertions.
// Production runtime operations use typed SDK options.
func prefixedShellFragment(flag string, args ...string) string {
	parts := make([]string, 0, 1+len(args))
	if strings.TrimSpace(flag) != "" {
		parts = append(parts, flag)
	}
	for _, arg := range args {
		if strings.TrimSpace(arg) != "" {
			parts = append(parts, arg)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func shellCommandArgsFragment(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return " -- " + shellArgsFragment(args)
}

func runtimeNodeValues(project Project, node Node) (map[string]string, error) {
	image := strings.TrimSpace(node.Image)
	if image == "" {
		image = strings.TrimSpace(project.DefaultImage)
	}
	if image == "" {
		return nil, invalidArgument("node image is required", map[string]any{"node": node.ID})
	}
	ports, err := validatePorts(node.Ports)
	if err != nil {
		return nil, err
	}
	policyFlags, err := renderNetPolicyFlags(node.NetPolicy)
	if err != nil {
		return nil, err
	}
	mountFlags := ""
	if nodeWorkspaceMode(node) == WorkspaceModeMounted {
		hostPath := strings.TrimSpace(node.WorkspaceMountPath)
		if hostPath == "" {
			hostPath = project.WorkspacePath
		}
		if hostPath == "" || node.GuestWorkspacePath == "" {
			return nil, invalidArgument("mounted workspace requires host and guest paths", map[string]any{"node": node.ID})
		}
		mountFlags = prefixedShellFragment("--mount-dir", shellQuote(hostPath+":"+node.GuestWorkspacePath))
	}
	portFlags := ""
	for _, port := range ports {
		portFlags += prefixedShellFragment("--port", shellQuote(port))
	}
	return map[string]string{
		"sandbox_name": shellQuote(node.SandboxName),
		"image":        shellQuote(image),
		"mount_flags":  mountFlags,
		"port_flags":   portFlags,
		"net_flags":    policyFlags,
	}, nil
}

func renderNetPolicyFlags(policy *NetPolicy) (string, error) {
	if policy == nil {
		return "", nil
	}
	defaultAction := strings.ToLower(strings.TrimSpace(policy.Default))
	if defaultAction != "allow" && defaultAction != "deny" {
		return "", invalidArgument("net policy default must be allow or deny", map[string]any{"default": policy.Default})
	}
	flags := prefixedShellFragment("--net-default", shellQuote(defaultAction))
	for _, target := range policy.Allow {
		target = strings.TrimSpace(target)
		if target == "" || strings.ContainsAny(target, "\r\n") {
			return "", invalidArgument("net policy allow target is invalid", map[string]any{"target": target})
		}
		flags += prefixedShellFragment("--net-rule", shellQuote("allow@"+target))
	}
	return flags, nil
}
