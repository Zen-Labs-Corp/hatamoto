package entity

// NodeSwitches say whether the default WebPage and BreadcrumbList JSON-LD
// nodes are emitted. nil keeps today's behaviour for that node.
type NodeSwitches struct{ WebPage, Breadcrumb *bool }

// ResolveNodeSwitches combines the schema settings with an entry's attributes:
// an entry "on"/"off" wins over the schema, which wins over the default (nil).
// Any other value reads as absent.
func ResolveNodeSwitches(st SchemaURLSettings, attrs map[string]any) NodeSwitches {
	return NodeSwitches{
		WebPage:    resolveSwitch(st.EmitWebPage, attrs[attrJSONLDWebPage]),
		Breadcrumb: resolveSwitch(st.EmitBreadcrumb, attrs[attrJSONLDBreadcrumb]),
	}
}

func resolveSwitch(schema NodeSwitch, entry any) *bool {
	if s, ok := entry.(string); ok {
		if v, ok := switchValue(NodeSwitch(s)); ok {
			return &v
		}
	}
	if v, ok := switchValue(schema); ok {
		return &v
	}
	return nil
}

func switchValue(s NodeSwitch) (bool, bool) {
	switch s {
	case NodeSwitchOn:
		return true, true
	case NodeSwitchOff:
		return false, true
	}
	return false, false
}
