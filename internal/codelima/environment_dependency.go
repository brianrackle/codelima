package codelima

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// This is advisory source evidence, never an observation of the guest. The
// image, earlier environments, sourced scripts and dynamic shell code can
// provide commands that this deliberately bounded analysis cannot establish.
type environmentDependencyReport struct {
	Warnings      []environmentDependencyWarning
	Unavailable   []int
	CurlInstalled bool
}
type environmentDependencyWarning struct {
	Command int
	Fix     *environmentCurlInstall
}
type environmentCurlInstall struct {
	Command int
	Offset  int
	Source  string
}

type curlFacts struct {
	reachable bool
	installed bool
	fix       *environmentCurlInstall
}
type curlFlow struct{ success, failure curlFacts }
type environmentDependencyAnalyzer struct {
	report  environmentDependencyReport
	command int
	source  string
}

func analyzeEnvironmentDependencies(commands []string) environmentDependencyReport {
	a := environmentDependencyAnalyzer{}
	facts := curlFacts{reachable: true}
	remaining := 256 << 10
	for i, source := range commands {
		a.command, a.source = i, source
		if i >= 256 || len(source) > remaining {
			a.report.Unavailable = append(a.report.Unavailable, i)
			facts = curlFacts{reachable: true}
			break
		}
		if len(source) > 64<<10 {
			a.report.Unavailable = append(a.report.Unavailable, i)
			facts = curlFacts{reachable: true}
			continue
		}
		remaining -= len(source)
		file, err := syntax.NewParser().Parse(strings.NewReader(source), "")
		if err != nil {
			a.report.Unavailable = append(a.report.Unavailable, i)
			facts = curlFacts{reachable: true}
			continue
		}
		// Each saved command block must succeed before bootstrap runs the next.
		facts = a.statements(file.Stmts, facts).success
	}
	a.report.CurlInstalled = facts.reachable && facts.installed
	return a.report
}

func mergeCurlFacts(a, b curlFacts) curlFacts {
	if !a.reachable {
		return b
	}
	if !b.reachable {
		return a
	}
	result := curlFacts{reachable: true, installed: a.installed && b.installed}
	// A suggested install must precede the use on every analyzed path.
	if a.fix != nil && b.fix != nil && *a.fix == *b.fix {
		result.fix = a.fix
	}
	return result
}

func mergeCurlFlows(a, b curlFlow) curlFlow {
	return curlFlow{mergeCurlFacts(a.success, b.success), mergeCurlFacts(a.failure, b.failure)}
}

func (a *environmentDependencyAnalyzer) statements(stmts []*syntax.Stmt, input curlFacts) curlFlow {
	flow := curlFlow{success: input}
	for _, stmt := range stmts {
		flow = a.statement(stmt, mergeCurlFacts(flow.success, flow.failure))
	}
	return flow
}

func (a *environmentDependencyAnalyzer) statement(stmt *syntax.Stmt, input curlFacts) curlFlow {
	if stmt == nil || !input.reachable {
		return curlFlow{}
	}
	for _, redirect := range stmt.Redirs {
		a.substitutions(redirect.Word, input)
		a.substitutions(redirect.Hdoc, input)
	}
	flow := a.commandFlow(stmt.Cmd, input)
	if stmt.Background || stmt.Coprocess {
		// A background install cannot order a following command's use.
		return curlFlow{success: input}
	}
	if stmt.Negated {
		flow.success, flow.failure = flow.failure, flow.success
	}
	return flow
}

func (a *environmentDependencyAnalyzer) commandFlow(command syntax.Command, input curlFacts) curlFlow {
	switch c := command.(type) {
	case *syntax.CallExpr:
		return a.call(c, input)
	case *syntax.Block:
		return a.statements(c.Stmts, input)
	case *syntax.Subshell:
		return a.statements(c.Stmts, input)
	case *syntax.BinaryCmd:
		left := a.statement(c.X, input)
		switch c.Op {
		case syntax.AndStmt:
			right := a.statement(c.Y, left.success)
			return curlFlow{right.success, mergeCurlFacts(left.failure, right.failure)}
		case syntax.OrStmt:
			right := a.statement(c.Y, left.failure)
			return curlFlow{mergeCurlFacts(left.success, right.success), right.failure}
		default:
			// Pipelines execute concurrently; lexical order is not install order.
			a.statement(c.Y, input)
			return curlFlow{input, input}
		}
	case *syntax.IfClause:
		condition := a.statements(c.Cond, input)
		then := a.statements(c.Then, condition.success)
		otherwise := curlFlow{success: condition.failure}
		if c.Else != nil {
			if len(c.Else.Cond) == 0 {
				otherwise = a.statements(c.Else.Then, condition.failure)
			} else {
				otherwise = a.commandFlow(c.Else, condition.failure)
			}
		}
		return mergeCurlFlows(then, otherwise)
	case *syntax.FuncDecl:
		// A declaration is not an execution of its body.
		return curlFlow{success: input}
	case nil:
		return curlFlow{success: input}
	default:
		// Loops/case/time and other compounds may execute calls, but do not
		// establish an unconditional install for a later command.
		syntax.Walk(command, func(node syntax.Node) bool {
			switch n := node.(type) {
			case *syntax.FuncDecl:
				return false
			case *syntax.CallExpr:
				a.call(n, input)
				return false
			}
			return true
		})
		return curlFlow{input, input}
	}
}

func (a *environmentDependencyAnalyzer) substitutions(word *syntax.Word, input curlFacts) {
	if word == nil {
		return
	}
	syntax.Walk(word, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CmdSubst:
			a.statements(n.Stmts, input)
			return false
		case *syntax.ProcSubst:
			a.statements(n.Stmts, input)
			return false
		}
		return true
	})
}

func (a *environmentDependencyAnalyzer) call(call *syntax.CallExpr, input curlFacts) curlFlow {
	for _, assignment := range call.Assigns {
		a.substitutions(assignment.Value, input)
	}
	for _, word := range call.Args {
		a.substitutions(word, input)
	}
	flow := curlFlow{input, input}
	if len(call.Args) == 0 {
		return flow
	}
	args := make([]string, len(call.Args))
	static := true
	for i, word := range call.Args {
		var ok bool
		args[i], ok = staticShellWord(word)
		static = static && ok
	}
	args = dependencyCommandArgs(args)
	if len(args) == 0 {
		return flow
	}
	switch args[0] {
	case "true", ":":
		return curlFlow{success: input}
	case "false":
		return curlFlow{failure: input}
	case "curl", "/usr/bin/curl", "/bin/curl":
		if !input.installed {
			a.warn(input.fix)
		}
	}
	if !static {
		return flow
	}
	action, packages, ok := literalAPTCommand(args)
	if !ok {
		return flow
	}
	curl := slices.ContainsFunc(packages, func(p string) bool {
		return p == "curl" || p == "curl+" || strings.HasPrefix(p, "curl=")
	})
	remove := action != "install" && curl || slices.Contains(packages, "curl-")
	if remove {
		flow.success.installed, flow.success.fix = false, nil
	} else if action == "install" {
		if curl {
			flow.success.installed = true
		} else {
			site := &environmentCurlInstall{a.command, int(call.Args[len(call.Args)-1].End().Offset()), a.source}
			flow.success.fix, flow.failure.fix = site, site
		}
	}
	return flow
}

func (a *environmentDependencyAnalyzer) warn(fix *environmentCurlInstall) {
	for i := range a.report.Warnings {
		warning := &a.report.Warnings[i]
		if warning.Command != a.command {
			continue
		}
		if warning.Fix != nil && (fix == nil || *warning.Fix != *fix) {
			warning.Fix = nil
		}
		return
	}
	a.report.Warnings = append(a.report.Warnings, environmentDependencyWarning{a.command, fix})
}

func dependencyCommandArgs(args []string) []string {
	for len(args) > 0 {
		switch args[0] {
		case "command", "exec":
			args = args[1:]
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) > 0 && strings.HasPrefix(args[0], "-") {
				return nil // command -v/-V inspect a name instead of executing it.
			}
		case "sudo":
			args = args[1:]
			for len(args) > 0 && (args[0] == "-H" || args[0] == "-n" || args[0] == "--") {
				last := args[0] == "--"
				args = args[1:]
				if last {
					break
				}
			}
			if len(args) > 0 && strings.HasPrefix(args[0], "-") {
				return nil
			}
		default:
			return args
		}
	}
	return nil
}

func literalAPTCommand(args []string) (string, []string, bool) {
	if len(args) < 3 || args[0] != "apt-get" && args[0] != "apt" {
		return "", nil, false
	}
	var action string
	var packages []string
	for _, arg := range args[1:] {
		if arg == "" {
			return "", nil, false
		}
		if strings.HasPrefix(arg, "-") {
			switch arg {
			case "-y", "--yes", "--assume-yes", "-q", "-qq", "-yq", "-qy", "--no-install-recommends", "--no-install-suggests", "--reinstall":
				continue
			default:
				// Simulation, download-only, dynamic -o configuration and
				// unknown flags cannot establish installation evidence.
				return "", nil, false
			}
		}
		if action == "" {
			if arg != "install" && arg != "remove" && arg != "purge" {
				return "", nil, false
			}
			action = arg
		} else {
			packages = append(packages, arg)
		}
	}
	return action, packages, action != "" && len(packages) > 0
}

func staticShellWord(word *syntax.Word) (string, bool) {
	return staticShellParts(word.Parts, false)
}

func staticShellParts(parts []syntax.WordPart, quoted bool) (string, bool) {
	var result strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			for i := 0; i < len(p.Value); i++ {
				c := p.Value[i]
				if c == '\\' && i+1 < len(p.Value) {
					next := p.Value[i+1]
					if !quoted || next == 0x60 || strings.ContainsRune("$\"\\\n", rune(next)) {
						if next != '\n' {
							result.WriteByte(next)
						}
						i++
						continue
					}
				}
				if !quoted && strings.ContainsRune("*?[{~", rune(c)) {
					return "", false
				}
				result.WriteByte(c)
			}
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			result.WriteString(p.Value)
		case *syntax.DblQuoted:
			if p.Dollar {
				return "", false
			}
			value, ok := staticShellParts(p.Parts, true)
			if !ok {
				return "", false
			}
			result.WriteString(value)
		default:
			return "", false
		}
	}
	return result.String(), true
}

func applyEnvironmentDependencyFix(commands []string, warning environmentDependencyWarning) ([]string, bool) {
	fix := warning.Fix
	if fix == nil || fix.Command < 0 || fix.Command >= len(commands) {
		return nil, false
	}
	source := commands[fix.Command]
	if source != fix.Source || fix.Offset < 0 || fix.Offset > len(source) {
		return nil, false
	}
	draft := slices.Clone(commands)
	draft[fix.Command] = source[:fix.Offset] + " curl" + source[fix.Offset:]
	return draft, true
}
