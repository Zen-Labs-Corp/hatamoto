package installstats

import "encoding/json"

// Payload is the whole request body. It has exactly these three fields; adding
// one changes what an owner agreed to, so it needs a new consent text.
type Payload struct {
	Install string `json:"install"`
	Version string `json:"version"`
	Channel string `json:"channel"`
}

// Pretty is the body indented for display; it decodes to the same JSON as the
// wire body.
func (p Payload) Pretty() string {
	b, _ := json.MarshalIndent(p, "", "  ")
	return string(b)
}
