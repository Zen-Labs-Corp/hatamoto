package sdk

// EntityTerm is one term assignment in the `terms` of an entities.get result
// or an entities.list item. The host adds `terms` only for a plugin whose sdk
// range requires 0.20 or later and that holds terms.read; every other plugin
// gets the entity without the key. For an entitled plugin an entity with no
// terms has `"terms": []`. Terms are ordered by kind, then name. EntityEvent
// does not carry terms: read them with entities.get.
type EntityTerm struct {
	Kind string `json:"kind"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// EntityTermFilter is the `term` argument of entities.list. It needs
// terms.read (otherwise the call fails with ErrCapabilityDenied) and keeps
// entities carrying exactly this term, not entities carrying a descendant.
// A term that does not exist yields an empty page, and a malformed kind fails
// with ErrInvalidArgument. In a run's fetch phase only public entities match.
type EntityTermFilter struct {
	Kind string `json:"kind"`
	Slug string `json:"slug"`
}
