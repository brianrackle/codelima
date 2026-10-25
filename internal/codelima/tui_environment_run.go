package codelima

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type tuiEnvironmentTestIntent struct {
	request    NodeOperationRequest
	generation uint64
	pending    bool
}

type tuiEnvironmentTestAdmissionEvent struct {
	intent     *tuiEnvironmentTestIntent
	record     NodeOperation
	err        error
	definitive bool
}

func (a *vaxisTUIApp) submitEnvironmentTest(env EnvironmentConfig) error {
	if a.service == nil || a.service.daemonClient == nil {
		return errors.New("a daemon connection is required to test an environment")
	}
	if a.recipes.tests == nil {
		a.recipes.tests = make(map[string]*tuiEnvironmentTestIntent)
	}
	intent := a.recipes.tests[env.ID]
	if intent == nil {
		intent = &tuiEnvironmentTestIntent{request: NodeOperationRequest{ID: newID(), Kind: "test-environment", Origin: "tui", Environment: env.ID}}
		a.recipes.tests[env.ID] = intent
	}
	if intent.pending {
		return nil
	}
	intent.pending, intent.generation = true, a.managerGeneration
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	client, request := a.service.daemonClient, intent.request
	a.setFooterStatus(slog.LevelInfo, "Starting disposable test of "+env.Slug+" · output in Activity")
	submit := func() tuiEnvironmentTestAdmissionEvent {
		defer cancel()
		e := tuiEnvironmentTestAdmissionEvent{intent: intent}
		e.err = client.Call(ctx, "operation.submit", request, &e.record)
		var rpcError *daemon.RPCError
		e.definitive = errors.As(e.err, &rpcError)
		e.err = fromDaemonError(e.err)
		return e
	}
	if a.postEvent == nil {
		a.applyEnvironmentTestAdmission(submit())
		return nil
	}
	go func() { a.postCompletion(submit()) }()
	return nil
}

func (a *vaxisTUIApp) applyEnvironmentTestAdmission(e tuiEnvironmentTestAdmissionEvent) {
	i := e.intent
	if i == nil || a.recipes.tests[i.request.Environment] != i || !i.pending {
		return
	}
	i.pending = false
	if e.err != nil {
		if e.definitive {
			delete(a.recipes.tests, i.request.Environment)
			a.setFooterStatus(slog.LevelError, "Environment test: "+e.err.Error())
		} else {
			a.setFooterStatus(slog.LevelWarn, fmt.Sprintf("Admission unconfirmed · %s · t on this environment retries the same request", i.request.ID))
		}
		return
	}
	if e.record.ID != i.request.ID {
		return
	}
	delete(a.recipes.tests, i.request.Environment)
	a.setFooterStatus(slog.LevelInfo, "Environment test accepted · continues after quit · see Activity")
	if a.view == tuiViewTemplates && a.environmentsView && a.overlay == nil && a.managerGeneration == i.generation &&
		a.environmentIndex >= 0 && a.environmentIndex < len(a.managerData.environments) && a.managerData.environments[a.environmentIndex].ID == i.request.Environment {
		a.switchView(tuiViewActivity)
		a.activity.selected = e.record.ID
		a.openOperationOutput(e.record.ID, e.record)
	}
}
