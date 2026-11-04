//go:build wasip1

// Command cronrun is the test plugin of cron-started runs: both phases finish
// at once without staging anything, so the run ends and its changeset is left
// to the auto-publish gate.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

func init() {
	guest.OnRunStep(func(step sdk.RunStep) (sdk.RunStepResult, error) {
		if _, err := guest.RunInfoHost(); err != nil {
			panic(err)
		}
		return sdk.RunStepResult{}, guest.RunFinishHost(nil)
	})
}

//go:wasmexport on_run_step
func onRunStep() int32 { return guest.Run(sdk.RunStepExport) }
