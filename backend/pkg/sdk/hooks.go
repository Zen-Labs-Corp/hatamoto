package sdk

import "strings"

// Action is a typed name token for a fire-and-forget hook. Actions run
// asynchronously after the event happened; a plugin's failure cannot affect
// the originating request. A plugin handles action "entity.published" by
// exporting the function "on_entity_published".
type Action[T any] struct{ Name string }

// Export is the wasm export a plugin provides to handle the action.
func (a Action[T]) Export() string { return ExportName("on_", a.Name) }

// Filter is a typed name token for a hook that transforms a value. Filters
// run synchronously in ascending priority order. A plugin handles filter
// "jsonld.enrich" by exporting "filter_jsonld_enrich": it receives the value
// as JSON and returns the (possibly modified) value as JSON.
type Filter[T any] struct{ Name string }

// Export is the wasm export a plugin provides to handle the filter.
func (f Filter[T]) Export() string { return ExportName("filter_", f.Name) }

// ExportName derives an export name from a hook name: the prefix followed by
// the hook name with every "." replaced by "_".
func ExportName(prefix, hook string) string {
	return prefix + strings.ReplaceAll(hook, ".", "_")
}

// Built-in hooks. Hook names and payload shapes are part of the SDK's
// compatibility surface (see docs/PLUGIN_API.md).
var (
	EntityCreated   = Action[EntityEvent]{"entity.created"}
	EntityUpdated   = Action[EntityEvent]{"entity.updated"}
	EntityPublished = Action[EntityEvent]{"entity.published"}
	EntityDeleted   = Action[EntityEvent]{"entity.deleted"}

	// ImportCompleted fires once after an import batch was stored. Imported
	// rows fire no entity.* action: a plugin that must see them all reacts
	// here and lists what changed through its host API.
	ImportCompleted = Action[ImportEvent]{"import.completed"}

	// AgentHit fires for every stored known-agent hit (any purpose; filter on
	// Purpose). The payload carries no IP address.
	AgentHit = Action[AgentHitEvent]{"agent.hit"}
	// AgentFirstCrawl fires once per entity, on the first served AI fetch
	// after it was recorded.
	AgentFirstCrawl = Action[AgentFirstCrawlEvent]{"agent.first_crawl"}

	// FormSubmitted fires after a public form submission was stored. It carries
	// the submitted fields, so it reaches only plugins granted forms.read.
	FormSubmitted = Action[FormSubmittedEvent]{"form.submitted"}

	JSONLDEnrich = Filter[JSONLDDoc]{"jsonld.enrich"}
	SitemapURLsF = Filter[SitemapURLs]{"sitemap.urls"}

	// RenderHead lets a plugin add tags to the <head> of server-rendered
	// pages. Tags are structured; the host builds and escapes the markup.
	RenderHead = Filter[HeadContext]{"render.head"}

	// RenderSiteGraph lets a plugin extend the generic site graph (since
	// 0.16.0): add keys to the Organization, LocalBusiness and WebSite nodes
	// and append nodes. It never changes an existing key, @id or @type. Not
	// called when render.head replaced the graph.
	RenderSiteGraph = Filter[SiteGraph]{"render.site_graph"}

	// RenderPage is the request-aware page filter: it sees the page's blocks
	// and the cookies the plugin declared, and may replace or hide blocks and
	// set declared cookies. A page served through it is never shared-cached.
	RenderPage = Filter[PageContext]{"render.page"}

	// EntityBeforeSave runs before an entity is created, updated or restored.
	// Rejecting (see RejectPrefix) refuses the save with HTTP 422 and the
	// reason. Any other error, a trap or a timeout is logged and the filter's
	// change is dropped; the save proceeds (fail open, like every filter).
	EntityBeforeSave = Filter[EntityDraft]{"entity.before_save"}

	// BuilderData answers the data-source requests a page render collected:
	// the plugin receives only requests naming its own declared
	// builder_sources (ext.<plugin>.<name>) and fills Results for them. One
	// call serves a whole render, never one per item. Needs the builder.data
	// permission; without the grant the filter is not registered. Any error,
	// trap or timeout leaves the affected values empty and the render
	// degraded (fail open).
	BuilderData = Filter[BuilderDataRequest]{"builder.data"}
)

// ActionNames and FilterNames list every built-in hook, in a stable order.
var (
	ActionNames = []string{
		EntityCreated.Name, EntityUpdated.Name, EntityPublished.Name, EntityDeleted.Name, ImportCompleted.Name,
		AgentHit.Name, AgentFirstCrawl.Name, FormSubmitted.Name,
	}
	FilterNames = []string{
		JSONLDEnrich.Name, SitemapURLsF.Name, RenderHead.Name, RenderPage.Name, EntityBeforeSave.Name, BuilderData.Name, RenderSiteGraph.Name,
	}
)

// RejectPrefix starts the error message of a guest filter export that
// refuses the operation (entity.before_save only); the rest of the message is
// the reason surfaced to the API client. The guest package's Reject does this.
const RejectPrefix = "hatamoto:reject:"

// Reject is the host-side error for a refused save. Reason is surfaced to
// the API client.
type Reject struct{ Reason string }

func (e *Reject) Error() string { return "rejected: " + e.Reason }
