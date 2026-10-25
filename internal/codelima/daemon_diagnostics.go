package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

type daemonDiagnosticOptions struct {
	Output, TerminalID string
	SampleSeconds      int
}

type daemonDiagnosticResult struct {
	Directory    string `json:"directory"`
	Files        int    `json:"files"`
	FailedProbes int    `json:"failed_probes"`
	Summary      string `json:"summary"`
}

type diagnosticProbe struct {
	Name       string `json:"name"`
	Error      string `json:"error,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type daemonDiagnosticCapture struct {
	home     string
	options  daemonDiagnosticOptions
	dir      string
	probes   []diagnosticProbe
	files    int
	writeErr error
	call     func(context.Context, string, any) (json.RawMessage, error)
	run      func(context.Context, string, ...string) diagnosticCommandResult
}

func newDaemonDiagnosticCapture(home string, options daemonDiagnosticOptions) *daemonDiagnosticCapture {
	return &daemonDiagnosticCapture{
		home: home, options: options, run: runDiagnosticCommand,
		call: func(ctx context.Context, method string, params any) (json.RawMessage, error) {
			client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: home, Version: Version})
			if err != nil {
				return nil, err
			}
			defer func() { _ = client.Close() }()
			var data json.RawMessage
			err = client.Call(ctx, method, params, &data)
			return data, fromDaemonError(err)
		},
	}
}

// capture gathers evidence without starting Lima, claiming the input seat or
// attempting recovery. Each failed probe is retained and the others continue.
func (c *daemonDiagnosticCapture) capture(parent context.Context) (daemonDiagnosticResult, error) {
	if c.options.SampleSeconds < 0 || c.options.SampleSeconds > 30 {
		return daemonDiagnosticResult{}, invalidArgument("sample duration must be between 0 and 30 seconds", nil)
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	if err := c.createDirectory(); err != nil {
		return daemonDiagnosticResult{}, err
	}
	binary, _ := os.Executable()
	c.write("capture.txt", fmt.Appendf(nil, "captured_at_utc=%s\ncodelima_home=%s\ncodelima_binary=%s\nversion=%s\nhost_os=%s\nhost_arch=%s\n",
		time.Now().UTC().Format(time.RFC3339Nano), c.home, binary, Version, runtime.GOOS, runtime.GOARCH))
	if c.writeErr != nil {
		return daemonDiagnosticResult{Directory: c.dir}, c.writeErr
	}
	c.file("daemon.identity", daemon.HomePaths(c.home).Identity, false)
	c.file("session.json", daemon.HomePaths(c.home).Session, false)
	c.file("daemon.log.tail", filepath.Join(daemon.HomePaths(c.home).Dir, "daemon.log"), true)
	c.file("codelima.log.tail", filepath.Join(c.home, "_logs", "codelima.log"), true)
	c.file("codelima.log.1.tail", filepath.Join(c.home, "_logs", "codelima.log.1"), true)
	c.command(ctx, "uname", 5*time.Second, "uname", "-a")
	c.command(ctx, "codelima-binary", 5*time.Second, "file", binary)
	if runtime.GOOS == "darwin" {
		c.command(ctx, "sw-vers", 5*time.Second, "sw_vers")
	}

	_, statusErr := c.rpc(ctx, "daemon-status", "daemon.status", nil)
	list, listErr := c.rpc(ctx, "terminal-list", "terminal.list", nil)
	snapshot, _ := c.rpc(ctx, "daemon-snapshot", "daemon.snapshot", nil)
	selected := c.options.TerminalID
	var terminals []daemon.TerminalState
	if selected == "" && json.Unmarshal(list, &terminals) == nil && len(terminals) > 0 {
		selected = terminals[0].TerminalID
	}
	var readErr error
	if selected != "" {
		c.write("probed-terminal-id.txt", []byte(selected+"\n"))
		_, readErr = c.rpc(ctx, "terminal-read", "terminal.read", map[string]any{"terminal_id": selected, "format": "text"})
	}
	var identity daemon.Identity
	if data, _, err := readDiagnosticFile(daemon.HomePaths(c.home).Identity, diagnosticFileLimit); err == nil {
		_ = json.Unmarshal(data, &identity)
	}
	if identity.PID > 0 {
		c.write("daemon-pid.txt", []byte(strconv.Itoa(identity.PID)+"\n"))
		c.process(ctx, "daemon", identity.PID)
	}
	c.renderers(ctx, snapshot, selected)

	classification := "The daemon control-plane probes failed; inspect the recorded errors, socket state and process evidence."
	if statusErr == nil && listErr == nil {
		switch {
		case selected == "":
			classification = "The daemon control plane responded; no terminal was available to probe."
		case readErr == nil:
			classification = "The daemon control plane and selected cached terminal read both responded; inspect renderer state/generation and then event delivery or client redraw handling. A cached read does not prove current rendering or input is healthy."
		default:
			classification = "The daemon control plane responded but the selected cached terminal read failed; inspect that terminal session, cache publisher and renderer worker."
		}
	}
	failed := 0
	for _, p := range c.probes {
		if p.Error != "" {
			failed++
		}
	}
	summary := fmt.Sprintf("# CodeLima terminal-freeze capture\n\nDirectory: %s\n\n%s\n\n%d probe failures are recorded in probes.json and individual .err files. Truncated evidence is marked in probes.json.\n\nThis bundle contains local logs, paths and terminal contents. No recovery action was performed.\n", c.dir, classification, failed)
	c.write("summary.md", []byte(summary))
	data, err := json.MarshalIndent(c.probes, "", "  ")
	if err == nil {
		c.write("probes.json", data)
	} else {
		c.writeErr = errors.Join(c.writeErr, err)
	}
	result := daemonDiagnosticResult{Directory: c.dir, Files: c.files, FailedProbes: failed, Summary: summary}
	if err := errors.Join(ctx.Err(), c.writeErr); err != nil {
		return result, externalCommandFailed("diagnostic capture incomplete", err, map[string]any{"directory": c.dir})
	}
	return result, nil
}

func (c *daemonDiagnosticCapture) createDirectory() error {
	if c.options.Output == "" {
		parent := filepath.Join(c.home, "_diagnostics")
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
		var err error
		c.dir, err = os.MkdirTemp(parent, "terminal-freeze-"+time.Now().UTC().Format("20060102T150405Z")+"-")
		return err
	}
	dir, err := filepath.Abs(c.options.Output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	c.dir = dir
	return nil
}

func (c *daemonDiagnosticCapture) write(name string, data []byte) {
	if c.writeErr != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(c.dir, name), data, 0o600); err != nil {
		c.writeErr = fmt.Errorf("write diagnostic %s: %w", name, err)
		return
	}
	c.files++
}

func (c *daemonDiagnosticCapture) record(name string, started time.Time, err error, truncated bool) {
	p := diagnosticProbe{Name: name, DurationMS: time.Since(started).Milliseconds(), Truncated: truncated}
	if err != nil {
		p.Error = err.Error()
	}
	c.probes = append(c.probes, p)
	c.write(name+".err", []byte(p.Error))
	code := 0
	if err != nil {
		code = exitCodeForError(err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	c.write(name+".exit", []byte(strconv.Itoa(code)+"\n"))
}

func (c *daemonDiagnosticCapture) file(name, path string, tail bool) {
	started := time.Now()
	var data []byte
	var truncated bool
	var err error
	if tail {
		var offset int64
		var missing bool
		data, offset, missing, err = readNodeLogTail(path, diagnosticLogLimit)
		truncated = offset > 0
		if missing {
			err = os.ErrNotExist
		}
	} else {
		data, truncated, err = readDiagnosticFile(path, diagnosticFileLimit)
	}
	if err == nil {
		c.write(name, data)
	}
	c.record(name, started, err, truncated)
}

func (c *daemonDiagnosticCapture) rpc(parent context.Context, name, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	started := time.Now()
	data, err := c.call(ctx, method, params)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	envelope := map[string]any{"ok": err == nil, "data": data}
	if err != nil {
		envelope["error"] = err.Error()
	}
	encoded, encodeErr := json.MarshalIndent(envelope, "", "  ")
	if encodeErr != nil {
		err = errors.Join(err, encodeErr)
	} else {
		c.write(name+".json", encoded)
	}
	c.record(name, started, err, false)
	return data, err
}

func (c *daemonDiagnosticCapture) command(parent context.Context, name string, timeout time.Duration, binary string, args ...string) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	started := time.Now()
	result := c.run(ctx, binary, args...)
	if ctx.Err() != nil {
		result.err = errors.Join(ctx.Err(), result.err)
	}
	c.write(name+".txt", result.stdout)
	if len(result.stderr) > 0 {
		c.write(name+".stderr", result.stderr)
	}
	c.record(name, started, result.err, result.truncated)
}

func (c *daemonDiagnosticCapture) process(ctx context.Context, name string, pid int) {
	id := strconv.Itoa(pid)
	c.command(ctx, name+"-ps", 5*time.Second, "ps", "-p", id, "-o", "pid,ppid,lstart,state,%cpu,%mem,comm")
	c.command(ctx, name+"-lsof", 5*time.Second, "lsof", "-p", id)
	if runtime.GOOS == "linux" {
		for _, file := range []string{"status", "limits", "stack"} {
			c.file(name+"-proc-"+file+".txt", filepath.Join("/proc", id, file), false)
		}
	}
	if runtime.GOOS == "darwin" && c.options.SampleSeconds > 0 {
		// sample normally creates an extra system-temp file. Give it an owned
		// path, retain only the bounded report, and remove its raw output.
		raw := filepath.Join(c.dir, "."+name+"-sample.raw")
		defer func() { _ = os.Remove(raw) }()
		c.command(ctx, name+"-sample-command", time.Duration(c.options.SampleSeconds+5)*time.Second, "sample", id, strconv.Itoa(c.options.SampleSeconds), "1", "-file", raw)
		c.file(name+"-sample.txt", raw, false)
	}
}

func (c *daemonDiagnosticCapture) renderers(ctx context.Context, data json.RawMessage, selected string) {
	var snapshot struct {
		Runtimes map[string]struct {
			PID int `json:"renderer_pid"`
		} `json:"terminal_runtimes"`
	}
	if json.Unmarshal(data, &snapshot) != nil {
		return
	}
	var pids []int
	for _, r := range snapshot.Runtimes {
		if r.PID > 0 {
			pids = append(pids, r.PID)
		}
	}
	slices.Sort(pids)
	pids = slices.Compact(pids)
	var ids []string
	for _, pid := range pids {
		ids = append(ids, strconv.Itoa(pid))
	}
	c.write("renderer-pids.txt", []byte(strings.Join(ids, "\n")+"\n"))
	if len(ids) > 0 {
		// One bounded process listing avoids per-renderer subprocess growth.
		c.command(ctx, "renderers-ps", 5*time.Second, "ps", "-p", strings.Join(ids[:min(len(ids), 128)], ","), "-o", "pid,ppid,lstart,state,%cpu,%mem,comm")
		if len(ids) > 128 {
			c.probes = append(c.probes, diagnosticProbe{Name: "renderers-ps", Truncated: true})
		}
	}
	if selectedPID := snapshot.Runtimes[selected].PID; selectedPID > 0 {
		c.write("probed-renderer-pid.txt", []byte(strconv.Itoa(selectedPID)+"\n"))
		c.process(ctx, "renderer", selectedPID)
	}
}
