package codelima

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"golang.org/x/crypto/ssh"
)

const (
	maxProcessOutput    = 2 << 20
	maxGuestProcesses   = 4096
	maxProcessRows      = 64
	guestProcessCommand = `printf 'codelima-clock '; getconf CLK_TCK
awk '{ print "codelima-uptime " $1 }' /proc/uptime
awk '{ print "codelima-load " $0 }' /proc/loadavg
awk '{ print "codelima-stat " $0 }' /proc/[0-9]*/stat 2>/dev/null
{ LC_ALL=C ps -eo pid=,user:32=,rss=,args= && printf 'codelima-complete\n'; } | awk '{ print "codelima-process " $0 }'`
)

type processCounters struct{ ticks, start uint64 }
type guestProcessObservation struct {
	clock     uint64
	uptime    float64
	load      [3]float64
	counters  map[int]processCounters
	rows      map[int]daemon.NodeProcess
	truncated bool
}

type forwardingProcessSampler interface {
	SampleProcesses(context.Context) (guestProcessObservation, error)
}

// A gate belongs to one connected peer. Only callers displaying Metrics request
// samples; simultaneous windows share at most one sample per second.
type processProbe struct {
	gate     chan struct{}
	at       time.Time
	previous *guestProcessObservation
	latest   daemon.NodeProcessSnapshot
	err      error
}

func (p *sshForwardingPeer) SampleProcesses(ctx context.Context) (guestProcessObservation, error) {
	data, err := p.runBounded(ctx, guestProcessCommand, maxProcessOutput)
	if err != nil {
		return guestProcessObservation{}, err
	}
	return parseGuestProcesses(data)
}

func (p *sshForwardingPeer) runBounded(ctx context.Context, command string, limit int) ([]byte, error) {
	// Opening a channel can itself stall on an unresponsive guest. Bound it
	// too, without closing the shared peer or disturbing forwarded traffic.
	type openedSession struct {
		session *ssh.Session
		err     error
	}
	opened := make(chan openedSession, 1)
	go func() { session, err := p.client.NewSession(); opened <- openedSession{session, err} }()
	var session *ssh.Session
	var err error
	select {
	case result := <-opened:
		session, err = result.session, result.err
	case <-ctx.Done():
		go func() {
			result := <-opened
			if result.session != nil {
				_ = result.session.Close()
			}
		}()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	stdout, stderr := &limitedBuffer{limit: limit + 1}, &limitedBuffer{limit: 64 << 10}
	session.Stdout, session.Stderr = stdout, stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err = <-done:
		if err != nil {
			return nil, fmt.Errorf("sample guest processes: %w", err)
		}
		if len(stdout.Bytes()) > limit {
			return nil, fmt.Errorf("guest process sample exceeds %d bytes", limit)
		}
		return stdout.Bytes(), nil
	}
}

func parseGuestProcesses(data []byte) (guestProcessObservation, error) {
	result := guestProcessObservation{counters: make(map[int]processCounters), rows: make(map[int]daemon.NodeProcess)}
	if len(data) > maxProcessOutput {
		return result, errors.New("guest process sample exceeds 2 MiB")
	}
	uptimeSeen, processesComplete, loadSeen := false, false, false
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "codelima-clock":
			result.clock, _ = strconv.ParseUint(fields[1], 10, 64)
		case "codelima-uptime":
			v, err := strconv.ParseFloat(fields[1], 64)
			if err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
				result.uptime = v
				uptimeSeen = true
			}
		case "codelima-load":
			if len(fields) < 4 {
				continue
			}
			loadSeen = true
			for i := range 3 {
				v, err := strconv.ParseFloat(fields[i+1], 64)
				if err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
					result.load[i] = v
				} else {
					loadSeen = false
				}
			}
		case "codelima-stat":
			pid, counter, ok := parseProcessCounters(strings.TrimPrefix(line, "codelima-stat "))
			if !ok {
				continue
			}
			if len(result.counters) >= maxGuestProcesses {
				result.truncated = true
				continue
			}
			result.counters[pid] = counter
		case "codelima-process":
			if fields[1] == "codelima-complete" {
				processesComplete = true
				continue
			}
			if len(fields) < 5 {
				continue
			}
			pid, err := strconv.Atoi(fields[1])
			if err != nil || pid <= 0 {
				continue
			}
			rss, err := strconv.ParseUint(fields[3], 10, 64)
			if err != nil || rss > math.MaxUint64/1024 {
				continue
			}
			if len(result.rows) >= maxGuestProcesses {
				result.truncated = true
				continue
			}
			result.rows[pid] = daemon.NodeProcess{PID: pid, User: processDisplayText(fields[2], 32), RSSBytes: rss * 1024, Command: processDisplayText(strings.Join(fields[4:], " "), 160)}
		}
	}
	if result.clock == 0 || result.clock > 1000000 || !uptimeSeen || !loadSeen || !processesComplete {
		return result, errors.New("guest process sample is incomplete")
	}
	return result, nil
}

func processDisplayText(value string, limit int) string {
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func parseProcessCounters(line string) (int, processCounters, bool) {
	open, close := strings.IndexByte(line, '('), strings.LastIndexByte(line, ')')
	if open < 1 || close <= open {
		return 0, processCounters{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	if err != nil || pid <= 0 {
		return 0, processCounters{}, false
	}
	fields := strings.Fields(line[close+1:])
	if len(fields) < 20 {
		return 0, processCounters{}, false
	}
	user, e1 := strconv.ParseUint(fields[11], 10, 64)
	system, e2 := strconv.ParseUint(fields[12], 10, 64)
	start, e3 := strconv.ParseUint(fields[19], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || user > math.MaxUint64-system {
		return 0, processCounters{}, false
	}
	return pid, processCounters{ticks: user + system, start: start}, true
}

func processSnapshot(current guestProcessObservation, previous *guestProcessObservation, at time.Time) daemon.NodeProcessSnapshot {
	result := daemon.NodeProcessSnapshot{SampledAt: at, Rows: make([]daemon.NodeProcess, 0, len(current.rows)), Load: current.load, Count: len(current.rows), Truncated: current.truncated}
	for pid, row := range current.rows {
		if previous != nil && previous.clock == current.clock {
			elapsed := current.uptime - previous.uptime
			before, knownBefore := previous.counters[pid]
			after, knownAfter := current.counters[pid]
			if knownBefore && knownAfter && before.start == after.start && after.ticks >= before.ticks && elapsed > 0 && elapsed <= 5 {
				percent := float64(after.ticks-before.ticks) / float64(current.clock) / elapsed * 100
				row.CPUPercent = &percent
			}
		}
		result.Rows = append(result.Rows, row)
	}
	return result
}

func sortedProcessSnapshot(value daemon.NodeProcessSnapshot, order string) daemon.NodeProcessSnapshot {
	pool := append(slices.Clone(value.Rows), value.AlternateRows...)
	value.Rows = sortedProcessRows(pool, order)
	opposite := "memory"
	if order == "memory" {
		opposite = "cpu"
	}
	leaders := sortedProcessRows(pool, opposite)
	seen := make(map[int]bool, len(value.Rows))
	for _, row := range value.Rows {
		seen[row.PID] = true
	}
	value.AlternateRows = nil
	for _, row := range leaders {
		if !seen[row.PID] {
			value.AlternateRows = append(value.AlternateRows, row)
		}
	}
	value.Truncated = value.Truncated || len(pool) > maxProcessRows
	return value
}

func sortedProcessRows(rows []daemon.NodeProcess, order string) []daemon.NodeProcess {
	rows = slices.Clone(rows)
	slices.SortFunc(rows, func(a, b daemon.NodeProcess) int {
		if order == "memory" {
			if a.RSSBytes > b.RSSBytes {
				return -1
			}
			if a.RSSBytes < b.RSSBytes {
				return 1
			}
		} else {
			if a.CPUPercent == nil && b.CPUPercent != nil {
				return 1
			}
			if a.CPUPercent != nil && b.CPUPercent == nil {
				return -1
			}
			if a.CPUPercent != nil && b.CPUPercent != nil {
				if *a.CPUPercent > *b.CPUPercent {
					return -1
				}
				if *a.CPUPercent < *b.CPUPercent {
					return 1
				}
			}
		}
		return a.PID - b.PID
	})
	rows = slices.Clone(rows[:min(len(rows), maxProcessRows)])
	for i := range rows {
		rows[i].CPUPercent = metricValueCopy(rows[i].CPUPercent)
	}
	return rows
}

func (f *dynamicForwarder) sampleProcesses(ctx context.Context, nodeID, order string) (daemon.NodeProcessSnapshot, error) {
	f.mu.Lock()
	state := f.nodes[nodeID]
	if state == nil || state.peer == nil {
		f.mu.Unlock()
		return daemon.NodeProcessSnapshot{}, errors.New("guest connection unavailable")
	}
	sampler, ok := state.peer.(forwardingProcessSampler)
	if !ok {
		f.mu.Unlock()
		return daemon.NodeProcessSnapshot{}, errors.New("guest process sampler unavailable")
	}
	if state.processes == nil {
		state.processes = &processProbe{gate: make(chan struct{}, 1)}
	}
	probe := state.processes
	f.mu.Unlock()
	select {
	case probe.gate <- struct{}{}:
	case <-ctx.Done():
		return daemon.NodeProcessSnapshot{}, ctx.Err()
	}
	defer func() { <-probe.gate }()
	if err := ctx.Err(); err != nil {
		return daemon.NodeProcessSnapshot{}, err
	}
	if !probe.at.IsZero() && f.nowUTC().Sub(probe.at) < time.Second {
		return sortedProcessSnapshot(probe.latest, order), probe.err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	current, err := sampler.SampleProcesses(ctx)
	probe.at = f.nowUTC()
	probe.err = err
	if err != nil {
		probe.previous = nil
		return daemon.NodeProcessSnapshot{}, err
	}
	f.mu.RLock()
	valid := f.nodes[nodeID] == state && state.processes == probe && state.peer != nil
	f.mu.RUnlock()
	if !valid {
		return daemon.NodeProcessSnapshot{}, errors.New("guest connection changed during process sample")
	}
	probe.latest = processSnapshot(current, probe.previous, probe.at)
	probe.previous = &current
	return sortedProcessSnapshot(probe.latest, order), nil
}
