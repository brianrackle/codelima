package ghostty

import (
	"io"
	"log/slog"
	"strings"

	"github.com/brianrackle/codelima/internal/terminalstate"
	"github.com/brianrackle/codelima/internal/testutil"
)

var (
	waitForCondition   = testutil.WaitForCondition
	newRenderTestVaxis = testutil.NewRenderVaxis
	renderedCellStyle  = testutil.RenderedCellStyle
	renderedScreenText = testutil.RenderedScreenText
	snapshotText       = terminalstate.SnapshotText
)

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'" }

func setPackageLogger(logger *slog.Logger) { slog.SetDefault(logger) }
func newTextLogger(writer io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level}))
}
func parseLogLevel(value string) slog.Level {
	if value == "debug" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}
