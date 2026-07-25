package codelima

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type daemonEventConnection interface {
	SubscribeSync(context.Context, []string) (daemon.SyncSnapshot, error)
	NextEvent(context.Context) (daemon.Event, error)
	Close() error
}

type daemonEventDialer func(context.Context) (daemonEventConnection, error)

type daemonConnectionState uint8

const (
	daemonConnectionDisconnected daemonConnectionState = iota
	daemonConnectionDialing
	daemonConnectionAuthenticating
	daemonConnectionSynchronizing
	daemonConnectionReady
)

type daemonConnectionStatus struct {
	State       daemonConnectionState
	Generation  uint64
	Epoch       string
	Sequence    uint64
	Err         error
	CloseRecord daemon.ConnectionCloseRecord
}

type daemonConnectionSupervisorOptions struct {
	Dial     daemonEventDialer
	OnSync   func(context.Context, daemon.SyncSnapshot) error
	OnEvent  func(daemon.Event)
	OnStatus func(daemonConnectionStatus)
	Sleep    func(context.Context, time.Duration) error
	Backoff  func(int) time.Duration
}

func runDaemonConnectionSupervisor(ctx context.Context, options daemonConnectionSupervisorOptions) error {
	if options.Dial == nil {
		return errors.New("daemon event dialer is required")
	}
	if options.Sleep == nil {
		options.Sleep = sleepDaemonReconnect
	}
	if options.Backoff == nil {
		options.Backoff = daemonReconnectBackoff
	}

	var generation uint64
	failures := 0
	for ctx.Err() == nil {
		generation++
		reportDaemonConnectionStatus(options.OnStatus, daemonConnectionStatus{
			State:      daemonConnectionDialing,
			Generation: generation,
		})
		connection, err := options.Dial(ctx)
		if err != nil {
			failures++
			reportDaemonConnectionStatus(options.OnStatus, daemonConnectionStatus{
				State:      daemonConnectionDisconnected,
				Generation: generation,
				Err:        err,
			})
			if sleepErr := options.Sleep(ctx, options.Backoff(failures)); sleepErr != nil {
				return context.Cause(ctx)
			}
			continue
		}

		reportDaemonConnectionStatus(options.OnStatus, daemonConnectionStatus{
			State:      daemonConnectionAuthenticating,
			Generation: generation,
		})
		snapshot, err := connection.SubscribeSync(ctx, []string{"terminal", "target", "node", "daemon"})
		if err == nil {
			reportDaemonConnectionStatus(options.OnStatus, daemonConnectionStatus{
				State:      daemonConnectionSynchronizing,
				Generation: generation,
				Epoch:      snapshot.DaemonEpoch,
				Sequence:   snapshot.StateSequence,
			})
			if options.OnSync != nil {
				err = options.OnSync(ctx, snapshot)
			}
		}
		if err != nil {
			_ = connection.Close()
			failures++
			reportDaemonConnectionStatus(options.OnStatus, daemonConnectionStatus{
				State:      daemonConnectionDisconnected,
				Generation: generation,
				Epoch:      snapshot.DaemonEpoch,
				Sequence:   snapshot.StateSequence,
				Err:        err,
			})
			if sleepErr := options.Sleep(ctx, options.Backoff(failures)); sleepErr != nil {
				return context.Cause(ctx)
			}
			continue
		}

		failures = 0
		epoch := snapshot.DaemonEpoch
		sequence := snapshot.StateSequence
		missedHeartbeats := 0
		reportDaemonConnectionStatus(options.OnStatus, daemonConnectionStatus{
			State:      daemonConnectionReady,
			Generation: generation,
			Epoch:      epoch,
			Sequence:   sequence,
		})

		for ctx.Err() == nil {
			event, readErr := connection.NextEvent(ctx)
			if readErr != nil {
				var netErr net.Error
				if errors.As(readErr, &netErr) && netErr.Timeout() {
					missedHeartbeats++
					if missedHeartbeats >= 3 {
						err = fmt.Errorf("daemon heartbeat missed %d consecutive read deadlines", missedHeartbeats)
						break
					}
					continue
				}
				err = readErr
				break
			}
			missedHeartbeats = 0
			if event.DaemonEpoch != epoch {
				err = fmt.Errorf("daemon event epoch changed from %q to %q", epoch, event.DaemonEpoch)
				break
			}
			if event.StateSequence <= sequence {
				continue
			}
			if event.StateSequence != sequence+1 {
				err = fmt.Errorf("daemon event sequence gap after %d: received %d", sequence, event.StateSequence)
				break
			}
			sequence = event.StateSequence
			if options.OnEvent != nil {
				options.OnEvent(event)
			}
			if event.Event == "daemon.shutdown" || event.Event == "daemon.update_committed" {
				err = fmt.Errorf("%s requested reconnect", event.Event)
				break
			}
		}
		_ = connection.Close()
		closeRecord := daemonConnectionCloseRecord(connection)
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		failures++
		reportDaemonConnectionStatus(options.OnStatus, daemonConnectionStatus{
			State:       daemonConnectionDisconnected,
			Generation:  generation,
			Epoch:       epoch,
			Sequence:    sequence,
			Err:         err,
			CloseRecord: closeRecord,
		})
		if sleepErr := options.Sleep(ctx, options.Backoff(failures)); sleepErr != nil {
			return context.Cause(ctx)
		}
	}
	return context.Cause(ctx)
}

func daemonConnectionCloseRecord(connection daemonEventConnection) daemon.ConnectionCloseRecord {
	if source, ok := connection.(interface {
		LastCloseRecord() daemon.ConnectionCloseRecord
	}); ok {
		return source.LastCloseRecord()
	}
	return daemon.ConnectionCloseRecord{}
}

func reportDaemonConnectionStatus(sink func(daemonConnectionStatus), status daemonConnectionStatus) {
	if sink != nil {
		sink(status)
	}
}

func daemonReconnectBackoff(failures int) time.Duration {
	if failures <= 1 {
		return 0
	}
	delay := 100 * time.Millisecond
	for step := 2; step < failures && delay < 5*time.Second; step++ {
		delay *= 2
	}
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	return time.Duration(rand.Int64N(int64(delay) + 1))
}

func sleepDaemonReconnect(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
