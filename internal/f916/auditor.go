package f916

import (
	"fmt"
)

type AuditResult struct {
	Valid         bool   `json:"valid"`
	TotalEvents   int    `json:"total_events"`
	BrokenAtEvent int64  `json:"broken_at_event,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	EarliestTime  int64  `json:"earliest_time,omitempty"`
	LatestTime    int64  `json:"latest_time,omitempty"`
}

func AuditModerationChain(events []ModerationEvent) AuditResult {
	if len(events) == 0 {
		return AuditResult{
			Valid:       true,
			TotalEvents: 0,
		}
	}

	earliest := events[0].CreatedAt
	latest := events[len(events)-1].CreatedAt

	for i := 1; i < len(events); i++ {
		prevEv := events[i-1]
		currEv := events[i]

		if currEv.PrevHash != nil && prevEv.Hash != nil {
			if *currEv.PrevHash != *prevEv.Hash {
				return AuditResult{
					Valid:         false,
					TotalEvents:   len(events),
					BrokenAtEvent: currEv.ID,
					ErrorMessage:  fmt.Sprintf("Hash chain mismatch at event #%d: expected prev_hash %s, got %s", currEv.ID, *prevEv.Hash, *currEv.PrevHash),
					EarliestTime:  earliest,
					LatestTime:    latest,
				}
			}
		}
	}

	return AuditResult{
		Valid:        true,
		TotalEvents:  len(events),
		EarliestTime: earliest,
		LatestTime:   latest,
	}
}
