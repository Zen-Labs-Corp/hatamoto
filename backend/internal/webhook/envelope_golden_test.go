package webhook

import (
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// Hard-coded bytes of every webhook body as the previous encoder wrote them,
// with the receiver signatures over them. The body is stored, signed and
// re-sent verbatim, so a drift here breaks every receiver: HTML characters and
// U+2028 stay escaped, map keys stay sorted, nil data stays null.

var (
	goldenNow    = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	goldenSecret = "whsec_pin"
	goldenUnix   = int64(1700000000)
)

const tricky = "Tom & <Jerry> \u2028 café"

type goldenCase struct {
	name  string
	event string
	data  func(t *testing.T) any
}

func mapped(name string, p any) func(t *testing.T) any {
	return func(t *testing.T) any {
		t.Helper()
		data, ok := mapPayload(name, p)
		if !ok {
			t.Fatalf("mapPayload(%s) refused the payload", name)
		}
		return data
	}
}

func goldenCases() []goldenCase {
	entity := sdk.EntityEvent{ID: "e-1", SchemaSlug: "post", Slug: "tom", Locale: "fr-CA", Name: tricky, Status: "published", Version: 3}
	touch := &sdk.FormTouch{Channel: "organic", Source: "example.org/?a=1&b=2", Medium: "<web>", Campaign: "spring \u2028 sale", Landing: "/café"}
	return []goldenCase{
		{"created", EventEntityCreated, mapped(sdk.EntityCreated.Name, entity)},
		{"deleted", EventEntityDeleted, mapped(sdk.EntityDeleted.Name, entity)},
		{"updated", EventEntityUpdated, mapped(sdk.EntityUpdated.Name, entity)},
		{"published", EventEntityPublished, mapped(sdk.EntityPublished.Name, entity)},
		{"form without attribution", EventFormSubmitted, mapped(sdk.FormSubmitted.Name,
			sdk.FormSubmittedEvent{FormID: "f-1", FormSlug: "contact", SubmissionID: "s-1", Fields: map[string]any{"email": "x@y.z"}})},
		{"form with attribution", EventFormSubmitted, mapped(sdk.FormSubmitted.Name,
			sdk.FormSubmittedEvent{FormID: "f-1", FormSlug: "contact", SubmissionID: "s-1", Attribution: &sdk.FormAttribution{First: touch, Last: &sdk.FormTouch{Channel: "direct"}}})},
		{"import", EventImportCompleted, mapped(sdk.ImportCompleted.Name, sdk.ImportEvent{Source: "wp & <co>", Created: 4, Updated: 2, Failed: 1})},
		{"nil data", EventEntityCreated, func(*testing.T) any { return nil }},
		{"test event", EventTest, func(*testing.T) any { return map[string]string{"message": "test delivery from Hatamoto"} }},
	}
}

var goldenWant = map[string]struct{ body, sig, sigV2 string }{
	"created": {
		"{\"id\":\"id-created\",\"event\":\"entity.created\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"entity_id\":\"e-1\",\"entity_name\":\"Tom \\u0026 \\u003cJerry\\u003e \\u2028 café\",\"schema_slug\":\"post\",\"locale\":\"fr-CA\"}}",
		"sha256=c6409f98d29a0512f5533489b5e7a2d6b9c92336c569a683091f3f3ddad775c0",
		"t=1700000000,v1=8fc66945b70bee4b94f7aac88edcf0b18dceb94757b5f2277ff799bc08867018",
	},
	"deleted": {
		"{\"id\":\"id-deleted\",\"event\":\"entity.deleted\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"entity_id\":\"e-1\",\"entity_name\":\"Tom \\u0026 \\u003cJerry\\u003e \\u2028 café\",\"schema_slug\":\"post\",\"locale\":\"fr-CA\"}}",
		"sha256=d3e6c680d6d5281d5cf38fdac37a9512e0c8272b14a3932389b9159f55b67468",
		"t=1700000000,v1=ddd6037b0821d8119d214530f39ca9c2697c56e726b61105d6e6f647106443c8",
	},
	"updated": {
		"{\"id\":\"id-updated\",\"event\":\"entity.updated\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"entity_id\":\"e-1\",\"entity_name\":\"Tom \\u0026 \\u003cJerry\\u003e \\u2028 café\",\"version\":3,\"locale\":\"fr-CA\"}}",
		"sha256=5c972acd6bd61fca7bdc01f3081a12d92f37c9ad53a3b0ae64105578f1b0844c",
		"t=1700000000,v1=c037ed33ff3b375a20f7eed91ecabe0a9c4617665058d85fe5833a5d13d73ea9",
	},
	"published": {
		"{\"id\":\"id-published\",\"event\":\"entity.published\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"entity_id\":\"e-1\",\"entity_name\":\"Tom \\u0026 \\u003cJerry\\u003e \\u2028 café\",\"locale\":\"fr-CA\"}}",
		"sha256=69ebbf0052000eec636ff07febdbd29681a790af2b2ae0790563636ae157d168",
		"t=1700000000,v1=ef5c3eea0f3c5308252663eb4e8ec77113ca08dadaeb3cb9743c8ca1af9d7b03",
	},
	"form without attribution": {
		"{\"id\":\"id-form without attribution\",\"event\":\"form.submitted\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"form_id\":\"f-1\",\"form_slug\":\"contact\",\"submission_id\":\"s-1\"}}",
		"sha256=52883c8029a0b0edd5bb774a5a2c24b7c84dbeedc7ebaf23bdab96eb67db1e7d",
		"t=1700000000,v1=35d5d3b46a60712fac55948619bee0d4de1219249a708b120f2de9b9399defa7",
	},
	"form with attribution": {
		"{\"id\":\"id-form with attribution\",\"event\":\"form.submitted\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"form_id\":\"f-1\",\"form_slug\":\"contact\",\"submission_id\":\"s-1\",\"attribution\":{\"first\":{\"channel\":\"organic\",\"source\":\"example.org/?a=1\\u0026b=2\",\"medium\":\"\\u003cweb\\u003e\",\"campaign\":\"spring \\u2028 sale\",\"landing\":\"/café\"},\"last\":{\"channel\":\"direct\"}}}}",
		"sha256=abc41c89f2904b7f0198b588bf37f5dd1732e50e7ab907876a5b62c3dc548fb8",
		"t=1700000000,v1=a477d176c1a09e0cbf6390beeaf8181b7d6f50e559dd1eabf9623b20f0c25881",
	},
	"import": {
		"{\"id\":\"id-import\",\"event\":\"import.completed\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"created\":4,\"failed\":1,\"source\":\"wp \\u0026 \\u003cco\\u003e\",\"updated\":2}}",
		"sha256=58707e3de92956142bd710c1f70ca056af3ee942b33e0bd2bcf74842b67c9ee5",
		"t=1700000000,v1=eac86a0aebed2a496e866c4934c1f6783c0a989762e119325fd4b541aec8924c",
	},
	"nil data": {
		"{\"id\":\"id-nil data\",\"event\":\"entity.created\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":null}",
		"sha256=d6fe77d05795dea20fc62d2c44939d079e5757f9fd3cdf98c8bbd43157c0b4bd",
		"t=1700000000,v1=2b0272812def3449b5cd01775c4abb585b5d6e32e513f4fc015f70a14a46bf2a",
	},
	"test event": {
		"{\"id\":\"id-test event\",\"event\":\"webhook.test\",\"timestamp\":\"2026-10-19T12:00:00Z\",\"data\":{\"message\":\"test delivery from Hatamoto\"}}",
		"sha256=0ea40ee5c7b645bf66d6ca45cd6784daab295817cae53ea0b19304ae2451e787",
		"t=1700000000,v1=19212e36db5c4205aa09a94ab46f822e7d9f331f2fa9b01d5a5a08d3364df590",
	},
}

func TestEnvelopeBytesGolden(t *testing.T) {
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			got, err := NewEnvelope("id-"+c.name, c.event, goldenNow, c.data(t))
			if err != nil {
				t.Fatal(err)
			}
			if want := goldenWant[c.name].body; string(got) != want {
				t.Fatalf("body changed:\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestEnvelopeSignaturesGolden(t *testing.T) {
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			body := []byte(goldenWant[c.name].body)
			if got, want := Sign(goldenSecret, body), goldenWant[c.name].sig; got != want {
				t.Errorf("Sign = %s, want %s", got, want)
			}
			if got, want := SignV2(goldenSecret, goldenUnix, body), goldenWant[c.name].sigV2; got != want {
				t.Errorf("SignV2 = %s, want %s", got, want)
			}
		})
	}
}
