package codelima

import (
	"context"
	"fmt"
	"strings"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

type tuiCloneOptions struct {
	names      map[string]bool
	loaded     bool
	err        error
	nameEdited bool
	start      bool
	checkFocus bool
}

type tuiCloneNamesEvent struct {
	review *tuiNodeConfirmation
	nodes  []Node
	err    error
}

func (a *vaxisTUIApp) openCloneNodeDialog(node Node) {
	d := a.newNodeConfirmation(node, tuiActionNodeClone, nil)
	d.run = func(current Node) error {
		input := NodeCloneInput{SourceNode: current.ID, NodeSlug: d.cloneName(), Start: d.clone.start}
		return a.startOperation(tuiOperationRequest{
			Title: "Cloning node " + current.Slug, DisplayStatus: "cloning",
			ResourceKeys: []string{terminal.NodeTarget(current.ID).String()},
			EntryKeys:    []string{terminal.NodeTarget(current.ID).String()},
			Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				child, err := service.NodeClone(ctx, input)
				if err != nil {
					return tuiOperationResult{}, err
				}
				status := "cloned node " + current.Slug + " to " + child.Slug
				if input.Start {
					status += " and started it"
				}
				return tuiOperationResult{Status: status, PreferredKey: terminal.NodeTarget(child.ID).String(), ReloadData: true}, nil
			},
		})
	}
	a.overlay = d
	// The fleet may be directory-filtered. Read the complete name index off
	// the event loop; the service still owns final uniqueness at admission.
	if a.service == nil {
		return
	}
	service := a.service
	load := func() tuiCloneNamesEvent {
		nodes, err := service.store.ListNodes(false)
		return tuiCloneNamesEvent{review: d, nodes: nodes, err: err}
	}
	if a.postEvent == nil {
		a.applyCloneNames(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyCloneNames(e tuiCloneNamesEvent) {
	d := e.review
	if a.overlay != d || d.clone == nil {
		return
	}
	d.clone.err, d.clone.loaded = e.err, e.err == nil
	if e.err != nil {
		return
	}
	d.clone.names = make(map[string]bool, len(e.nodes)+1)
	for _, node := range e.nodes {
		if node.DeletedAt == nil {
			d.clone.names[node.Slug] = true
		}
	}
	d.clone.names[d.node.Slug] = true
	if !d.clone.nameEdited && d.cloneName() == "" {
		for suffix := 2; ; suffix++ {
			name := fmt.Sprintf("%s-%d", d.node.Slug, suffix)
			if !d.clone.names[name] {
				d.dialog.SetFieldValue("node_slug", name)
				d.dialog.Error = ""
				break
			}
		}
	}
}

func (d *tuiNodeConfirmation) cloneName() string {
	return strings.TrimSpace(d.dialog.Fields[0].Input.String())
}

func (d *tuiNodeConfirmation) cloneNameError() error {
	if d.clone.err != nil {
		return fmt.Errorf("check names: %w; reopen to retry", d.clone.err)
	}
	if !d.clone.loaded {
		return fmt.Errorf("checking names…")
	}
	name := d.cloneName()
	if name == "" || slugify(name) != name {
		return fmt.Errorf("use a lowercase slug")
	}
	if d.clone.names[name] {
		return fmt.Errorf("that name is already used")
	}
	return nil
}

func (d *tuiNodeConfirmation) updateCloneInput(event vaxis.Event) {
	if d.clone.checkFocus {
		return
	}
	before := d.dialog.Fields[0].Input.String()
	d.dialog.updateInput(event)
	if d.dialog.Fields[0].Input.String() != before {
		d.clone.nameEdited = true
		d.dialog.Error = ""
	}
}
