package builder

import (
	"sync/atomic"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// registry is one immutable snapshot of every node kind and its compiled prop
// schema. Readers load it without locking; a writer builds a new one and
// swaps the pointer, so a call that takes one snapshot never sees a mix.
type registry struct {
	kinds   map[string]kindSpec
	schemas map[string]*jsonschema.Schema
}

// reg is the live snapshot: nativeKinds plus the embedded widgets.
var reg atomic.Pointer[registry]

func init() {
	coreRegistry = newRegistry(mergeWidgets(nativeKinds))
	reg.Store(coreRegistry)
}

// newRegistry compiles the schema of every kind in ks.
func newRegistry(ks map[string]kindSpec) *registry {
	return &registry{kinds: ks, schemas: compileKinds(ks)}
}

// kindOf returns the spec of a kind; the zero spec and false when unknown.
func kindOf(name string) (kindSpec, bool) {
	k, ok := reg.Load().kinds[name]
	return k, ok
}

// TakesChildren reports whether nodes of a kind can hold children.
func TakesChildren(kind string) bool {
	k, _ := kindOf(kind)
	return k.children
}

// schemaOf returns the compiled prop schema of a kind, nil when unknown.
func schemaOf(name string) *jsonschema.Schema {
	return reg.Load().schemas[name]
}

// coreRegistry is the snapshot without plugin kinds; SetPackKinds builds on it.
var coreRegistry *registry
