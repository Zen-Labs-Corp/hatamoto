package kit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/sdk"
)

// Action is what Apply will do with one item.
type Action string

// The actions of a plan step.
const (
	ActionCreate  Action = "create"  // not on the site yet
	ActionInstall Action = "install" // a package that is not installed yet
	ActionKeep    Action = "keep"    // already there, left alone
	ActionFill    Action = "fill"    // exists; only missing settings are added
	ActionSkip    Action = "skip"    // not applicable here, with a message
)

// Options say how the kit package was verified.
type Options struct {
	// Origin is what VerifyKit classified the kit's signer as.
	Origin packages.Origin
	// Tier is the registry's label for the kit when it is listed ("" = derive
	// it from Origin).
	Tier string
}

// PackageStep is a plugin or theme the kit needs.
type PackageStep struct {
	Kind    packages.Kind
	Name    string
	Version string // the registry's version, or the installed one for a keep
	Action  Action
	// Asked is every permission a plugin asks for; Grant is what the kit
	// grants on its own, Extra what is left for the owner's ticks.
	Asked, Grant, Extra []string
	// EntitySchema is the content type a plugin creates itself (manifest entity_schema).
	EntitySchema string
	// Activate is a theme's request to become the active theme (an owner tick).
	Activate bool
	// Raw is the package to install (nil for a keep).
	Raw []byte
}

// ItemStep is a content type, page or menu of the kit.
type ItemStep struct {
	Key    string // schema slug, page slug or menu location
	Name   string
	Action Action
	Note   string
	// Owner is the kit plugin whose entity_schema creates this content type.
	Owner string
}

// CommentsStep is the kit's request to turn comments on.
type CommentsStep struct {
	Requested, AlreadyOn bool
	// Warnings are the two texts the owner must accept first.
	Warnings [2]string
	// NoAddress means the site has no address, so comments cannot go on yet.
	NoAddress bool
}

// PolicyStep is the kit's request for the community policy on its content types.
type PolicyStep struct {
	Requested []string // types the owner's tick turns on (policy not on yet)
	AlreadyOn []string // requested types whose policy is already on: kept
	// Available is set when at least one type can be turned on; Warning and
	// Tick are the texts the owner must accept first (ADR 0005 9.5).
	Available bool
	Warning   string
	Tick      string
	Message   string
}

// HomepageStep is the kit's request to make one of its pages the homepage.
type HomepageStep struct{ Page string }

// Plan is what applying a kit would do. Building it writes nothing.
type Plan struct {
	Kit *packages.Kit
	// Digest is the kit package's signed digest.
	Digest [32]byte
	Signer string // key id of the signature, "" when unsigned
	Tier   string // first-party, developer or community (or the registry's label)
	// Fresh is a site on the default theme with the first-boot home page.
	Fresh   bool
	Applied *Applied

	Plugins, Themes       []PackageStep
	Schemas, Pages, Menus []ItemStep
	Comments              CommentsStep
	Policy                PolicyStep
	Homepage              HomepageStep

	// Kept lists what is already on the site and stays as it is.
	Kept []string
	// Messages are notes for the owner that do not stop the apply.
	Messages []string
	// Refusals stop the apply before any write.
	Refusals []string
}

// BuildPlan reads the site and the registry and returns what applying p would
// do (the type is Plan, so the constructor cannot share its name). p is a kit package that Manager.VerifyKit accepted.
func BuildPlan(ctx context.Context, d Deps, p *packages.Package, o Options) (*Plan, error) {
	k, err := p.Kit()
	if err != nil {
		return nil, err
	}
	pl := &Plan{Kit: k, Digest: p.Digest, Tier: tierOf(o)}
	if p.Signature != nil {
		pl.Signer = p.Signature.Signer
	}
	if a, err := d.Applied.Get(ctx, k.Name); err == nil {
		pl.Applied = a
	} else if !errors.Is(err, ErrNotApplied) {
		return nil, err
	}

	if ok, err := sdk.Satisfies(sdk.Version, k.SDK); err != nil || !ok {
		pl.refuse("this kit needs SDK %s, this site has %s", k.SDK, sdk.Version)
	}
	if missing := Missing(k.Requires); len(missing) > 0 {
		pl.refuse("this site does not have the features the kit requires: %s", strings.Join(missing, ", "))
	}
	if err := pl.planFresh(ctx, d); err != nil {
		return nil, err
	}

	var activeLocs []string
	if len(k.Plugins)+len(k.Themes) > 0 {
		idx, err := d.Packages.Registry(ctx)
		if err != nil {
			return nil, err
		}
		if err := pl.planPlugins(ctx, d, idx); err != nil {
			return nil, err
		}
		if activeLocs, err = pl.planThemes(ctx, d, idx); err != nil {
			return nil, err
		}
	}
	if err := pl.planSchemas(ctx, d); err != nil {
		return nil, err
	}
	if err := pl.planPages(ctx, d); err != nil {
		return nil, err
	}
	if err := pl.planMenus(ctx, d, activeLocs); err != nil {
		return nil, err
	}
	if err := pl.planSwitches(ctx, d); err != nil {
		return nil, err
	}
	return pl, nil
}

func (pl *Plan) refuse(format string, a ...any) {
	pl.Refusals = append(pl.Refusals, fmt.Sprintf(format, a...))
}
func (pl *Plan) note(format string, a ...any) {
	pl.Messages = append(pl.Messages, fmt.Sprintf(format, a...))
}

// tierOf is the signer label shown to the owner: the registry's tier when it
// lists one, else first-party, developer or community from the signer.
func tierOf(o Options) string {
	if o.Tier != "" {
		return o.Tier
	}
	switch o.Origin {
	case packages.OriginFirstParty:
		return "first-party"
	case packages.OriginDeveloper:
		return "developer"
	}
	return "community"
}

// planFresh sets Fresh: the default theme and the first-boot home page.
func (pl *Plan) planFresh(ctx context.Context, d Deps) error {
	theme, err := d.Packages.ActiveTheme(ctx)
	if err != nil {
		return err
	}
	home, err := d.Site.Homepage(ctx)
	if err != nil {
		return err
	}
	pl.Fresh = (theme == "" || theme == packages.DefaultTheme) && home != nil && starter.IsSeed(home)
	return nil
}

// resolve finds name in the registry, checks the range and downloads and
// parses the package. A nil package with refusals recorded means "refused".
func (pl *Plan) resolve(ctx context.Context, d Deps, idx *packages.Index, kind packages.Kind, name, rng string) (*packages.Package, packages.Entry, error) {
	e, ok := idx.Find(kind, name)
	if !ok {
		pl.refuse("%s %s is not listed in the registry", kind, name)
		return nil, e, nil
	}
	if ok, err := sdk.Satisfies(e.Version, rng); err != nil || !ok {
		pl.refuse("%s %s %s is outside the range %s the kit asks for", kind, name, e.Version, rng)
		return nil, e, nil
	}
	raw, err := d.Packages.Fetch(ctx, kind, name)
	if err != nil {
		return nil, e, err
	}
	p, err := packages.ParseNew(raw, packages.DefaultLimits)
	if err != nil {
		pl.refuse("%s %s: %v", kind, name, err)
		return nil, e, nil
	}
	if p.Kind != kind || p.Name != name {
		pl.refuse("the registry served %s %s for %s %s", p.Kind, p.Name, kind, name)
		return nil, e, nil
	}
	return p, e, nil
}

// installState says whether the package is already installed: keep it (in
// range), refuse (out of range) or install it.
func (pl *Plan) installState(ctx context.Context, d Deps, kind packages.Kind, name, rng string) (Action, string, error) {
	v, ok, err := d.Packages.Installed(ctx, kind, name)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return ActionInstall, "", nil
	}
	if in, err := sdk.Satisfies(v, rng); err != nil || !in {
		pl.refuse("%s %s %s is installed but the kit asks for %s: update %s first", kind, name, v, rng, name)
		return ActionSkip, v, nil
	}
	return ActionKeep, v, nil
}

func (pl *Plan) planPlugins(ctx context.Context, d Deps, idx *packages.Index) error {
	for _, kp := range pl.Kit.Plugins {
		p, e, err := pl.resolve(ctx, d, idx, packages.KindPlugin, kp.Name, kp.Range)
		if err != nil {
			return err
		}
		if p == nil {
			continue
		}
		man, err := p.PluginManifest()
		if err != nil {
			pl.refuse("plugin %s: %v", kp.Name, err)
			continue
		}
		act, ver, err := pl.installState(ctx, d, packages.KindPlugin, kp.Name, kp.Range)
		if err != nil {
			return err
		}
		if act == ActionSkip {
			continue
		}
		st := PackageStep{Kind: packages.KindPlugin, Name: kp.Name, Version: e.Version, Action: act, Asked: man.Asked(), EntitySchema: man.EntitySchema}
		if act == ActionKeep {
			st.Version = ver
		} else {
			st.Raw = p.Data()
		}
		var notAsked []string
		st.Grant, st.Extra, notAsked = GrantFor(st.Asked, kp.Grant)
		for _, n := range notAsked {
			pl.note("The kit lists permission %s for %s, which does not ask for it.", n, kp.Name)
		}
		pl.Plugins = append(pl.Plugins, st)
	}
	return nil
}

// planThemes plans the kit's themes and returns the menu locations the themes
// it would activate declare.
func (pl *Plan) planThemes(ctx context.Context, d Deps, idx *packages.Index) ([]string, error) {
	var locs []string
	for _, kt := range pl.Kit.Themes {
		p, e, err := pl.resolve(ctx, d, idx, packages.KindTheme, kt.Name, kt.Range)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		act, ver, err := pl.installState(ctx, d, packages.KindTheme, kt.Name, kt.Range)
		if err != nil {
			return nil, err
		}
		if act == ActionSkip {
			continue
		}
		st := PackageStep{Kind: packages.KindTheme, Name: kt.Name, Version: e.Version, Action: act, Activate: kt.Activate}
		if act == ActionKeep {
			st.Version = ver
		} else {
			st.Raw = p.Data()
		}
		if kt.Activate {
			locs = append(locs, themeLocations(p)...)
		}
		pl.Themes = append(pl.Themes, st)
	}
	return locs, nil
}

// themeLocations are the non-core menu locations a theme declares.
func themeLocations(p *packages.Package) []string {
	var meta struct {
		Menus jsontext.Value `json:"menus"`
	}
	if jsonx.Unmarshal(p.Manifest, &meta, jsonx.StoredLoose) != nil {
		return nil
	}
	decl, err := menu.ParseDecl(meta.Menus)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range decl {
		if !menu.IsCoreLocation(l.Name) {
			out = append(out, l.Name)
		}
	}
	return out
}

func (pl *Plan) planSchemas(ctx context.Context, d Deps) error {
	owned := map[string]string{} // slug -> kit plugin whose manifest declares it
	for _, st := range pl.Plugins {
		if st.EntitySchema != "" {
			owned[st.EntitySchema] = st.Name
		}
	}
	for _, ks := range pl.Kit.Schemas {
		step := ItemStep{Key: ks.Slug, Name: ks.Name}
		if plug, ok := owned[ks.Slug]; ok {
			step.Action, step.Owner, step.Note = ActionFill, plug, "created by plugin "+plug
			pl.Schemas = append(pl.Schemas, step)
			continue
		}
		_, err := d.Schemas.GetSchemaBySlug(ctx, ks.Slug)
		switch {
		case errors.Is(err, entity.ErrSchemaNotFound):
			step.Action = ActionCreate
		case err != nil:
			return err
		default:
			origin, err := d.Schemas.SchemaOrigin(ctx, ks.Slug)
			if err != nil {
				return err
			}
			switch {
			case origin == "":
				step.Action = ActionKeep
				pl.Kept = append(pl.Kept, fmt.Sprintf("content type '%s'", ks.Slug))
			case pl.isKitPlugin(origin):
				step.Action, step.Owner, step.Note = ActionFill, origin, "created by plugin "+origin
			default:
				pl.refuse("the content type '%s' belongs to the plugin %s, which the kit does not install", ks.Slug, origin)
				step.Action = ActionSkip
			}
		}
		pl.Schemas = append(pl.Schemas, step)
	}
	return nil
}

func (pl *Plan) isKitPlugin(name string) bool {
	return slices.ContainsFunc(pl.Kit.Plugins, func(kp packages.KitPackage) bool { return kp.Name == name })
}

func (pl *Plan) planPages(ctx context.Context, d Deps) error {
	for _, kp := range pl.Kit.Pages {
		schema := kp.Schema
		if schema == "" {
			schema = "page"
		}
		e, err := d.Pages.GetPage(ctx, schema, kp.Slug)
		if err != nil {
			return err
		}
		step := ItemStep{Key: kp.Slug, Name: kp.Name, Action: ActionCreate}
		if e != nil {
			step.Action = ActionKeep
			pl.Kept = append(pl.Kept, fmt.Sprintf("page '%s'", kp.Slug))
		}
		pl.Pages = append(pl.Pages, step)
	}
	return nil
}

func (pl *Plan) planMenus(ctx context.Context, d Deps, kitThemeLocs []string) error {
	var offered []string
	for _, km := range pl.Kit.Menus {
		step := ItemStep{Key: km.Location, Name: km.Name}
		if offered == nil {
			locs, err := d.Packages.ActiveThemeLocations(ctx)
			if err != nil {
				return err
			}
			offered = append(append([]string{}, locs...), kitThemeLocs...)
		}
		if !menu.IsCoreLocation(km.Location) && !slices.Contains(offered, km.Location) {
			step.Action = ActionSkip
			step.Note = fmt.Sprintf("the active theme has no location '%s'", km.Location)
			pl.note("Menu '%s' skipped: the active theme has no location '%s'.", km.Name, km.Location)
			pl.Menus = append(pl.Menus, step)
			continue
		}
		_, err := d.Menus.GetMenuByLocation(ctx, km.Location)
		switch {
		case errors.Is(err, menu.ErrMenuNotFound):
			step.Action = ActionCreate
		case err != nil:
			return err
		default:
			step.Action = ActionKeep
			pl.Kept = append(pl.Kept, fmt.Sprintf("menu '%s'", km.Name))
		}
		pl.Menus = append(pl.Menus, step)
	}
	return nil
}

func (pl *Plan) planSwitches(ctx context.Context, d Deps) error {
	sw := pl.Kit.Switches
	if sw.Comments {
		pl.Comments = CommentsStep{Requested: true, Warnings: [2]string{comment.EnableWarning, comment.EnableConfirmWarning}}
		on, err := d.Comments.Enabled(ctx)
		if err != nil {
			return err
		}
		pl.Comments.AlreadyOn = on
		pl.Comments.NoAddress = d.Site.Address(ctx) == ""
	}
	if len(sw.CommentPolicy) > 0 {
		if err := pl.planPolicy(ctx, d, sw.CommentPolicy); err != nil {
			return err
		}
	}
	pl.Homepage.Page = sw.Homepage
	if pl.Homepage.Page == "" {
		for _, kp := range pl.Kit.Pages {
			if kp.Homepage {
				pl.Homepage.Page = kp.Slug
				break
			}
		}
	}
	return nil
}

// planPolicy sorts the kit's policy types into those the owner's tick turns on
// and those already on.
func (pl *Plan) planPolicy(ctx context.Context, d Deps, slugs []string) error {
	switch {
	case !slices.Contains(pl.Kit.Requires, "comment.policy"):
		pl.Policy.Requested = slugs
		pl.Policy.Message = "Community policy skipped: the kit does not list comment.policy in requires."
	case d.SchemaComments == nil:
		pl.Policy.Requested = slugs
		pl.Policy.Message = "Community policy skipped: this site cannot turn it on yet."
	default:
		for _, slug := range slugs {
			st, err := d.SchemaComments.SchemaComments(ctx, slug)
			switch {
			case errors.Is(err, entity.ErrSchemaNotFound):
				pl.Policy.Requested = append(pl.Policy.Requested, slug) // the schemas step creates it
			case err != nil:
				return err
			case st.Builtin:
				pl.note("Community policy skipped for '%s': built-in content types cannot use it.", slug)
			case st.Policy.Enabled:
				pl.Policy.AlreadyOn = append(pl.Policy.AlreadyOn, slug)
				pl.Kept = append(pl.Kept, fmt.Sprintf("community policy on '%s'", slug))
			default:
				pl.Policy.Requested = append(pl.Policy.Requested, slug)
			}
		}
		if pl.Policy.Available = len(pl.Policy.Requested) > 0; pl.Policy.Available {
			pl.Policy.Warning, pl.Policy.Tick = comment.PolicyConfirmWarning, comment.PolicyTick
		}
	}
	if pl.Policy.Message != "" {
		pl.note("%s", pl.Policy.Message)
	}
	return nil
}

// Fingerprint identifies what a plan resolved: the kit's name, version and
// digest, and for every plugin and theme its version, the hash of the package
// to install and the permissions it asks for. The review carries it and Apply
// refuses when a new plan differs, so the owner applies what they reviewed.
func (pl *Plan) Fingerprint() string {
	h := sha256.New()
	fmt.Fprintf(h, "kit %s %s %x\n", pl.Kit.Name, pl.Kit.Version, pl.Digest)
	for _, st := range slices.Concat(pl.Plugins, pl.Themes) {
		fmt.Fprintf(h, "%s %s %s %x %q\n", st.Kind, st.Name, st.Version, sha256.Sum256(st.Raw), st.Asked)
	}
	return hex.EncodeToString(h.Sum(nil))
}
