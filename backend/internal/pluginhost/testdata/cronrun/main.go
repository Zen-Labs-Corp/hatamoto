//go:build wasip1

// Command cronrun is the test plugin of cron-started runs: the fetch phase
// reads from the first host the run was given (a cron run has no owner to pick
// one) and fails the run if it has none or the host answers with an error;
// both phases then finish without staging anything, so the run ends and its
// changeset is left to the auto-publish gate.
package main

import (
	"fmt"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

func init() {
	guest.OnRunStep(func(step sdk.RunStep) (sdk.RunStepResult, error) {
		info, err := guest.RunInfoHost()
		if err != nil {
			panic(err)
		}
		if step.Phase == sdk.RunPhaseFetch {
			if len(info.Hosts) == 0 {
				return sdk.RunStepResult{}, guest.RunFailHost("the run has no hosts to fetch from")
			}
			var out struct {
				Status int `json:"status"`
			}
			if err := guest.Call(sdk.FnHTTPFetch, map[string]string{"url": "http://" + info.Hosts[0] + "/rates"}, &out); err != nil {
				return sdk.RunStepResult{}, guest.RunFailHost("fetch: " + err.Error())
			}
			if out.Status != 200 {
				return sdk.RunStepResult{}, guest.RunFailHost(fmt.Sprint("fetch status ", out.Status))
			}
		}
		return sdk.RunStepResult{}, guest.RunFinishHost(nil)
	})
}

//go:wasmexport on_run_step
func onRunStep() int32 { return guest.Run(sdk.RunStepExport) }
