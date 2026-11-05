//go:build wasip1

// Command runner is the test plugin of the run driver: a two-phase run that
// buffers records while fetching and stages them as changeset items while
// transforming. The run's params steer its behaviour: "schema" (the entity
// schema to stage into), "mode" ("fail" calls run.fail in the first step,
// "error" returns an error from it, "leak" marks the guest's memory and fails
// any later call that finds the mark, so a pooled instance is caught).
package main

import (
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

type params struct {
	Schema string `json:"schema"`
	Tag    string `json:"tag"`
	Mode   string `json:"mode"`
}

// marked is a guest global that only a "leak" run sets.
var marked bool

func init() {
	guest.OnRunStep(func(step sdk.RunStep) (sdk.RunStepResult, error) {
		if marked { // before RunInfoHost: an ordinary call outside a run reaches here too
			return sdk.RunStepResult{}, errors.New("guest memory leaked")
		}
		info, err := guest.RunInfoHost()
		if err != nil { // outside a run: trap, which the test uses to open the circuit breaker
			panic(err)
		}
		var p params
		_ = jsonv2.Unmarshal(info.Params, &p, jsonv2.MatchCaseInsensitiveNames(true))
		switch p.Mode {
		case "leak":
			marked = true
		case "error":
			return sdk.RunStepResult{}, errors.New("boom")
		case "fail":
			return sdk.RunStepResult{}, guest.RunFailHost("the source is unreachable")
		}
		if step.Phase == sdk.RunPhaseFetch {
			return sdk.RunStepResult{}, fetch(step, p)
		}
		return sdk.RunStepResult{}, transform(p)
	})
}

// fetch takes two steps: the first buffers the parent record and checkpoints,
// the second (resumed from the cursor) buffers the child and finishes.
func fetch(step sdk.RunStep, p params) error {
	if step.Cursor == "" {
		if err := guest.BufferPutHost("rec/1", []byte("Parent")); err != nil {
			return err
		}
		return guest.RunCheckpointHost("page-2")
	}
	if step.Cursor != "page-2" {
		return fmt.Errorf("unexpected cursor %q", step.Cursor)
	}
	if err := guest.BufferPutHost("rec/2", []byte("Child")); err != nil {
		return err
	}
	return guest.RunFinishHost(nil)
}

func transform(p params) error {
	keys, _, err := guest.BufferListHost("rec/", "", 10)
	if err != nil {
		return err
	}
	for i, k := range keys {
		val, found, err := guest.BufferGetHost(k)
		if err != nil || !found {
			return fmt.Errorf("buffer %q: %v", k, err)
		}
		in := sdk.EntityUpsert{SourceKey: p.Tag + "-" + k, Schema: p.Schema, Slug: p.Tag + fmt.Sprintf("-%d", i), Name: string(val)}
		if i > 0 {
			in.ParentKey = p.Tag + "-" + keys[0]
		}
		if _, err := guest.EntityUpsertHost(in); err != nil {
			return err
		}
	}
	if _, err := guest.RedirectUpsertHost(sdk.RedirectUpsert{SourceKey: p.Tag + "-r", From: "/" + p.Tag + "-old", To: "/" + p.Tag + "-0"}); err != nil {
		return err
	}
	return guest.RunFinishHost(map[string]int{"records": len(keys)})
}

//go:wasmexport on_run_step
func onRunStep() int32 { return guest.Run(sdk.RunStepExport) }
