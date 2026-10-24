package codelima

import "errors"

// readOutputAfter copies only the requested bounded tail, never the retained
// replay journal. Resize spans advance the cursor but carry no terminal bytes.
func (j *rendererJournal) readOutputAfter(after uint64, limit int) ([]byte, uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if after > j.nextID || limit <= 0 {
		return nil, after, errors.New("invalid terminal output cursor or limit")
	}
	through := after
	var data []byte
	for _, event := range j.events {
		if event.ID <= after {
			continue
		}
		first := event.FirstID
		if first == 0 {
			first = event.ID
		}
		if first > through+1 {
			return nil, after, errors.New("terminal output retention exceeded; detach and attach again")
		}
		if event.Type == "output" {
			if len(event.Data) > limit-len(data) {
				if len(data) == 0 {
					return nil, after, errors.New("terminal output chunk exceeds read limit")
				}
				break
			}
			data = append(data, event.Data...)
		}
		through = event.ID
	}
	if through == after && after != j.nextID {
		return nil, after, errors.New("terminal output retention exceeded; detach and attach again")
	}
	return data, through, nil
}
