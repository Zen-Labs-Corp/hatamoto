//go:build wasip1

// Command commentcheck is the test plugin of comment.check and the comment
// events: its behaviour is picked by the plugin config key "mode".
package main

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func main() {}

// receivedKeys lists the top-level JSON keys the host sent, sorted and comma-joined.
func receivedKeys() string {
	var m map[string]jsontext.Value
	if err := jsonv2.Unmarshal(guest.Input(), &m); err != nil {
		return "unreadable: " + err.Error()
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

func init() {
	guest.OnFilter(sdk.CommentCheckF, func(c sdk.CommentCheck) (sdk.CommentCheck, error) {
		mode, _ := guest.Config("mode")
		switch mode {
		case "add":
			c.Score, c.Reasons = 40, []string{"test-add"}
		case "lower":
			c.Score = -50
		case "huge":
			c.Score = 1 << 30
		case "many":
			for i := range 20 {
				c.Reasons = append(c.Reasons, strings.Repeat("r", 500)+strconv.Itoa(i))
			}
			c.Score = 1
		case "keys": // what the host sent, as the only reason
			c.Reasons = []string{receivedKeys()}
			c.Score = 1
		case "body": // the host must ignore both changes
			c.Body, c.BodyHash, c.Score = "changed", "x", 5
		case "trap":
			panic("trap")
		case "sleep":
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			}
		case "reject":
			return c, guest.Reject("no")
		case "empty":
		}
		return c, nil
	})
	// comment.draft: the config mode "draft-*" picks the answer.
	guest.OnFilter(sdk.CommentDraftF, func(d sdk.CommentDraft) (sdk.CommentDraft, error) {
		mode, _ := guest.Config("mode")
		switch mode {
		case "draft-name":
			d.AuthorName = "name-" + d.CommenterKey[:6]
		case "draft-echo": // what the host sent, as the name
			d.AuthorName = "schema=" + d.Schema + ";name=" + d.AuthorName + ";reject=" + d.Reject
		case "draft-reject":
			d.Reject = "Pick a name first"
		case "draft-reject-err":
			return d, guest.Reject("no")
		case "draft-reject-long":
			d.Reject = "a\x07" + strings.Repeat("\u00e9", 150)
		case "draft-empty":
			d.AuthorName = "  "
		case "draft-sleep":
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			}
		}
		return d, nil
	})
	guest.OnAction(sdk.CommentApproved, func(ev sdk.CommentEvent) error {
		if err := guest.Call(sdk.FnKVSet, map[string]any{"key": "k:" + ev.ID, "value": ev.CommenterKey}, nil); err != nil {
			return err
		}
		return guest.Call(sdk.FnKVSet, map[string]any{"key": "c:" + ev.ID, "value": ev.Status}, nil)
	})
	for _, a := range []sdk.Action[sdk.CommentEvent]{sdk.CommentCreated, sdk.CommentUpdated, sdk.CommentDeleted} {
		guest.OnAction(a, func(ev sdk.CommentEvent) error {
			return guest.Call(sdk.FnKVSet, map[string]any{"key": a.Name + ":" + ev.ID, "value": ev.CommenterKey}, nil)
		})
	}
}

//go:wasmexport filter_comment_check
func filterCommentCheck() int32 { return guest.Run(sdk.CommentCheckF.Export()) }

//go:wasmexport on_comment_approved
func onCommentApproved() int32 { return guest.Run(sdk.CommentApproved.Export()) }

//go:wasmexport filter_comment_draft
func filterCommentDraft() int32 { return guest.Run(sdk.CommentDraftF.Export()) }

//go:wasmexport on_comment_created
func onCommentCreated() int32 { return guest.Run(sdk.CommentCreated.Export()) }

//go:wasmexport on_comment_updated
func onCommentUpdated() int32 { return guest.Run(sdk.CommentUpdated.Export()) }

//go:wasmexport on_comment_deleted
func onCommentDeleted() int32 { return guest.Run(sdk.CommentDeleted.Export()) }
