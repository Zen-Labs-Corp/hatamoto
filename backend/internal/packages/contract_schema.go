package packages

import (
	"fmt"

	"hatamoto.dev/sdk"
)

// checkEntitySchemaShipped refuses a plugin whose manifest entity_schema is not
// one of the content types its package ships (schemas/<slug>.json without a
// sections/<slug>.html). Load cannot see the package, so only install checks it.
func checkEntitySchemaShipped(p *Package, man sdk.Manifest) error {
	if man.EntitySchema == "" {
		return nil
	}
	types, err := p.EntitySchemas()
	if err != nil {
		return err
	}
	for _, ct := range types {
		if ct.Slug == man.EntitySchema {
			return nil
		}
	}
	return &ParseError{Msg: fmt.Sprintf("plugin %q declares entity_schema %q but its package has no schemas/%s.json content type", p.Name, man.EntitySchema, man.EntitySchema)}
}
