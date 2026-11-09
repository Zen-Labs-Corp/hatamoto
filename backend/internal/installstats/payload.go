package installstats

// Payload is the Basic request body. It has exactly these three fields; adding
// one changes what an owner agreed to, so it needs a new consent text. The
// Detailed body is built by BuildDetailed.
type Payload struct {
	Install string `json:"install"`
	Version string `json:"version"`
	Channel string `json:"channel"`
}
