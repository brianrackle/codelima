package codelima

import "github.com/brianrackle/codelima/internal/testutil"

var (
	waitForCondition     = testutil.WaitForCondition
	newRenderTestVaxis   = testutil.NewRenderVaxis
	decodedVaxisInputKey = testutil.DecodedVaxisInputKey
	renderedCellGrapheme = testutil.RenderedCellGrapheme
	renderedCellStyle    = testutil.RenderedCellStyle
	renderedScreenText   = testutil.RenderedScreenText
)
