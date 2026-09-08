package terminalstate

import "testing"

func TestValidateInteractionCoordinatesBeforeNativeNarrowing(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, action := range []string{"press", "drag", "release", "cancel", "copy"} {
		for _, coordinate := range []int{-1, 65536, maxInt, -maxInt - 1} {
			for _, request := range []InteractionRequest{
				{Action: action, Col: coordinate},
				{Action: action, Row: coordinate},
			} {
				if err := ValidateInteraction(request); err == nil {
					t.Errorf("accepted out-of-domain coordinates: %+v", request)
				}
			}
		}
	}
	for _, coordinate := range []int{0, 65535} {
		if err := ValidateInteraction(InteractionRequest{Action: "drag", Col: coordinate, Row: coordinate}); err != nil {
			t.Errorf("valid native-domain coordinate %d: %v", coordinate, err)
		}
	}
}
