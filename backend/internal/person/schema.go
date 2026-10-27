package person

import "hatamoto.dev/core/internal/entity"

// CoreFields are the person type's own properties: a site can add fields next
// to them but never remove or retype them.
var CoreFields = entity.PersonCoreFields
