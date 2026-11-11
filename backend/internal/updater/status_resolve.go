package updater

import (
	"fmt"
	"time"
)

// SwitchResult is what the supervisor left behind for the target of the last
// apply. The zero value means nothing could be read.
type SwitchResult struct {
	Pending    bool      // a switch is still in progress
	Running    string    // running version
	Outcome    string    // newest event phase for the target: "success", "rolled_back", "failed", ""
	Error      string    // the failure the event recorded
	FinishedAt time.Time // when that event finished
}

// ResolveStatus settles a stored "applying" status once the supervisor has
// finished the switch. Any other status, or a switch still in progress or
// without a recorded outcome, is returned unchanged.
func ResolveStatus(st Status, r SwitchResult) Status {
	if st.Decision != KindApplying || st.Target == "" || r.Pending {
		return st
	}
	switch r.Outcome {
	case "success":
		if r.Running != st.Target {
			return st
		}
		st.Decision = KindUpToDate
		st.Reason = fmt.Sprintf("running %s (switched %s UTC, gate passed)", st.Target, r.FinishedAt.UTC().Format("15:04"))
	case "rolled_back", "failed":
		st.Decision = KindRolledBack
		st.Reason = switchOutcomeText[r.Outcome]
		if r.Error != "" {
			st.Reason = r.Error
		}
	}
	return st
}

var switchOutcomeText = map[string]string{
	"rolled_back": "the switch was rolled back",
	"failed":      "the switch failed",
}
