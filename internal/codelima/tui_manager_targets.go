package codelima

import (
	"fmt"
	"slices"
)

func (a *vaxisTUIApp) managerNode(id string) (Node, error) {
	for _, node := range a.state.nodes {
		if node.ID == id {
			return node, nil
		}
	}
	return Node{}, fmt.Errorf("node is no longer available in this scope")
}

func (a *vaxisTUIApp) openManagerNode(id string, tab tuiDetailTab) error {
	if _, err := a.managerNode(id); err != nil {
		return err
	}
	if !slices.ContainsFunc(a.state.entries, func(entry tuiTreeEntry) bool { return entry.node.ID == id }) {
		return fmt.Errorf("node is no longer selectable")
	}
	a.refreshFleet()
	a.fleet.setFilter("")
	a.fleet.selectID(id)
	a.selectFleetNode()
	a.openNodeDetail(tab)
	return nil
}

type tuiCommandTarget struct {
	rect tuiRect
	id   string
}
