package media

import "hatamoto.dev/core/authz"

// AuthzObject describes m to the authorizer: CreatedBy is the uploader ("" when unknown).
func (m *Media) AuthzObject() authz.Object {
	o := authz.Object{Kind: "media", ID: m.ID.String()}
	if m.UploadedBy != nil {
		o.CreatedBy = m.UploadedBy.String()
	}
	return o
}
