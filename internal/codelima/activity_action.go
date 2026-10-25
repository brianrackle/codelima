package codelima

import "strings"

type activityAction string

const (
	activityOpenTerminal activityAction = "terminal"
	activityOpenHost     activityAction = "host"
	activityOpenPorts    activityAction = "ports"
	activityOpenDaemon   activityAction = "daemon"
)

func validateActivityAction(request activityMessageRequest) error {
	switch request.Action {
	case "", activityOpenHost, activityOpenPorts, activityOpenDaemon:
		if request.TerminalID == "" {
			return nil
		}
	case activityOpenTerminal:
		id := request.TerminalID
		if request.NodeID != "" && len(id) > 0 && len(id) <= 128 && strings.IndexFunc(id, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
		}) < 0 {
			return nil
		}
	}
	return invalidArgument("invalid Activity navigation target", nil)
}
