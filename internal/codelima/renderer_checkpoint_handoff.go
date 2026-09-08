package codelima

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalstate"
)

const rendererRecoveryVersion = 1

var rendererHandoffRecoveryQuota = newRendererCheckpointQuota(daemon.MaxHandoffTotalRecoveryBytes)

type rendererRecoveryBundle struct {
	Version    int                     `json:"version"`
	TerminalID string                  `json:"terminal_id"`
	Checkpoint *rendererCheckpoint     `json:"checkpoint,omitempty"`
	Colors     *TerminalColors         `json:"colors,omitempty"`
	Journal    rendererJournalSnapshot `json:"journal"`
	SHA256     string                  `json:"sha256"`
}

func encodeRendererRecovery(terminalID string, checkpoint *rendererCheckpoint, journal rendererJournalSnapshot, colors ...*TerminalColors) ([]byte, error) {
	bundle := rendererRecoveryBundle{Version: rendererRecoveryVersion, TerminalID: terminalID, Checkpoint: checkpoint, Journal: journal}
	if len(colors) > 0 {
		bundle.Colors = terminalstate.CloneColors(colors[0])
	}
	if err := terminalstate.ValidateColors(bundle.Colors); err != nil {
		return nil, err
	}
	digest, err := bundle.digest()
	if err != nil {
		return nil, err
	}
	bundle.SHA256 = digest
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	if len(data) > daemon.MaxHandoffRecoveryBytesPerTerminal {
		return nil, errors.New("renderer recovery bundle exceeds handoff size bound")
	}
	return data, nil
}

func (b rendererRecoveryBundle) digest() (string, error) {
	b.SHA256 = ""
	data, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	if len(data) > daemon.MaxHandoffRecoveryBytesPerTerminal {
		return "", errors.New("renderer recovery bundle exceeds handoff size bound")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func decodeRendererRecovery(terminalID string, data []byte) (*rendererRecoveryBundle, error) {
	if len(data) == 0 || len(data) > daemon.MaxHandoffRecoveryBytesPerTerminal {
		return nil, errors.New("renderer recovery bundle has invalid size")
	}
	var bundle rendererRecoveryBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, err
	}
	if bundle.Version != rendererRecoveryVersion || bundle.TerminalID != terminalID {
		return nil, errors.New("renderer recovery bundle identity or version mismatch")
	}
	digest, err := bundle.digest()
	if err != nil {
		return nil, err
	}
	if digest != bundle.SHA256 {
		return nil, errors.New("renderer recovery bundle integrity mismatch")
	}
	if err := validateRecoveryJournal(bundle.Journal); err != nil {
		return nil, err
	}
	if err := terminalstate.ValidateColors(bundle.Colors); err != nil {
		return nil, err
	}
	if checkpoint := bundle.Checkpoint; checkpoint != nil {
		if err := checkpoint.validateIntegrity(terminalID); err != nil {
			return nil, err
		}
		if _, complete := bundle.Journal.tailAfter(checkpoint.AppliedEventID); !complete {
			return nil, errors.New("renderer recovery checkpoint tail is incomplete")
		}
		if bundle.Colors == nil {
			bundle.Colors = terminalstate.CloneColors(checkpoint.State.Colors)
		}
		if checkpoint.BuildID != rendererNativeBuildIdentity() || checkpoint.Policy != currentRendererCheckpointPolicy() {
			bundle.Checkpoint = nil
		}
	}
	return &bundle, nil
}

func validateRecoveryJournal(journal rendererJournalSnapshot) error {
	if !rendererValidPixels(journal.CellWidth, journal.CellHeight) {
		return errors.New("renderer recovery pixel geometry is invalid")
	}
	if journal.LastID >= rendererInputEventBit || !rendererValidGeometry(journal.Cols, journal.Rows) || journal.Bytes < 0 || journal.Bytes > defaultRendererJournalBytes || len(journal.Events) > 65536 {
		return errors.New("renderer recovery journal bounds are invalid")
	}
	bytes := 0
	previous := uint64(0)
	for index, event := range journal.Events {
		first := event.FirstID
		if first == 0 {
			first = event.ID
		}
		if event.ID == 0 || event.ID > journal.LastID || event.ID <= previous || first > event.ID || (event.Type != "output" && event.Type != "resize") || (event.Type == "output" && first != event.ID) {
			return errors.New("renderer recovery journal event is invalid")
		}
		if (index > 0 || !journal.Partial) && first > previous+1 {
			return errors.New("renderer recovery journal has an interior gap")
		}
		if event.Type == "resize" && (!rendererValidGeometry(event.Cols, event.Rows) || !rendererValidPixels(event.CellWidth, event.CellHeight)) {
			return errors.New("renderer recovery journal geometry is invalid")
		}
		bytes += rendererJournalEventBytes(event)
		if bytes > defaultRendererJournalBytes {
			return errors.New("renderer recovery journal exceeds retention bound")
		}
		previous = event.ID
	}
	if bytes != journal.Bytes || (len(journal.Events) > 0 && previous != journal.LastID) || (len(journal.Events) == 0 && journal.LastID != 0 && !journal.Partial) {
		return errors.New("renderer recovery journal watermark or accounting is invalid")
	}
	return nil
}

func journalFromRendererRecovery(snapshot rendererJournalSnapshot) *rendererJournal {
	journal := newRendererJournal(defaultRendererJournalBytes)
	journal.nextID, journal.cols, journal.rows, journal.partial, journal.bytes = snapshot.LastID, snapshot.Cols, snapshot.Rows, snapshot.Partial, snapshot.Bytes
	journal.cellWidth, journal.cellHeight = snapshot.CellWidth, snapshot.CellHeight
	journal.events = make([]rendererJournalEvent, len(snapshot.Events))
	for index, event := range snapshot.Events {
		event.Data = slices.Clone(event.Data)
		journal.events[index] = event
	}
	return journal
}
