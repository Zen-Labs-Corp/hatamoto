// Package entity provides a schema-driven JSON-LD generator that reads
// x-schema-org-type and x-schema-org-property annotations from JSON Schema
// definitions to produce structured data conforming to Schema.org vocabulary.
package entity

import (
	"encoding/json"
	"sort"
	"strings"
)

// SchemaProperty represents a parsed JSON Schema property with its semantic annotations.
type SchemaProperty struct {
	Type              string                     `json:"type"`
	Format            string                     `json:"format,omitempty"`
	Title             string                     `json:"title,omitempty"`
	SchemaOrgType     string                     `json:"x-schema-org-type,omitempty"`
	SchemaOrgProperty string                     `json:"x-schema-org-property,omitempty"`
	Properties        map[string]*SchemaProperty `json:"properties,omitempty"`
	Items             *SchemaProperty            `json:"items,omitempty"`
	Required          []string                   `json:"required,omitempty"`
}

// SchemaDefinition represents the top-level JSON Schema with semantic annotations.
type SchemaDefinition struct {
	Type          string                     `json:"type"`
	SchemaOrgType string                     `json:"x-schema-org-type,omitempty"`
	Properties    map[string]*SchemaProperty `json:"properties,omitempty"`
	Required      []string                   `json:"required,omitempty"`
}

// GenerateSchemaDrivernJSONLD produces JSON-LD by walking the schema annotations.
// It reads x-schema-org-type from the root to determine @type, then recursively
// maps attribute values using x-schema-org-property annotations on each property.
//
// Returns nil if the schema has no x-schema-org-type annotation (caller should
// fall back to the legacy switch-based generator).
func GenerateSchemaDrivernJSONLD(ent *Entity, baseURL string) map[string]any {
	if ent == nil || ent.Schema == nil {
		return nil
	}

	// Parse the schema definition
	var schemaDef SchemaDefinition
	if err := json.Unmarshal(ent.Schema.Schema, &schemaDef); err != nil {
		return nil
	}

	// If no x-schema-org-type is defined, signal that the legacy generator should handle it
	if schemaDef.SchemaOrgType == "" {
		return nil
	}

	// Parse entity attributes
	var attrs map[string]any
	if err := json.Unmarshal(ent.Attributes, &attrs); err != nil {
		attrs = make(map[string]any)
	}

	// Build the JSON-LD object
	jsonLD := map[string]any{
		"@context": "https://schema.org",
		"@type":    schemaDef.SchemaOrgType,
	}

	// Always include entity URL, @id, and dateModified
	entityURL := buildEntityURLForJSONLD(ent, baseURL)
	jsonLD["url"] = entityURL
	jsonLD["@id"] = entityURL + "#" + strings.ToLower(schemaDef.SchemaOrgType)
	jsonLD["dateModified"] = ent.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")

	// Add datePublished if available
	if ent.PublishedAt != nil {
		jsonLD["datePublished"] = ent.PublishedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	// Walk schema properties and map attribute values
	if schemaDef.Properties != nil {
		mapPropertiesToJSONLD(jsonLD, attrs, schemaDef.Properties, schemaDef.SchemaOrgType)
	}

	// For FAQ schema, the entity name should map to "name" if not already set
	if _, hasName := jsonLD["name"]; !hasName {
		if _, hasHeadline := jsonLD["headline"]; !hasHeadline {
			// Use the entity name as a fallback name/headline
			switch schemaDef.SchemaOrgType {
			case "Article":
				jsonLD["headline"] = ent.Name
			default:
				jsonLD["name"] = ent.Name
			}
		}
	}

	return jsonLD
}

// mapPropertiesToJSONLD walks schema properties and maps attribute values to JSON-LD keys.
func mapPropertiesToJSONLD(jsonLD map[string]any, attrs map[string]any, properties map[string]*SchemaProperty, parentType string) {
	for attrKey, prop := range properties {
		if prop.SchemaOrgProperty == "" {
			// No semantic mapping — check if this is a container object whose
			// nested properties have direct mappings (like contact.email → email).
			if prop.Type == "object" && prop.Properties != nil {
				if nestedAttrs, ok := attrs[attrKey].(map[string]any); ok {
					mapFlattenedPropertiesToJSONLD(jsonLD, nestedAttrs, prop.Properties)
				}
			}
			continue
		}

		attrValue, exists := attrs[attrKey]
		if !exists || attrValue == nil {
			continue
		}

		ldKey := prop.SchemaOrgProperty

		switch prop.Type {
		case "object":
			mapObjectProperty(jsonLD, ldKey, attrValue, prop)

		case "array":
			mapArrayProperty(jsonLD, ldKey, attrValue, prop, parentType)

		default:
			// Scalar values — handle special cases
			mapScalarProperty(jsonLD, ldKey, attrValue, prop)
		}
	}
}

// mapFlattenedPropertiesToJSONLD maps nested object properties directly to the parent JSON-LD
// when the container object has no x-schema-org-property. This handles patterns like
// contact.email → email on the parent, contact.phone → telephone on the parent.
func mapFlattenedPropertiesToJSONLD(jsonLD map[string]any, attrs map[string]any, properties map[string]*SchemaProperty) {
	for attrKey, prop := range properties {
		if prop.SchemaOrgProperty == "" {
			continue
		}
		attrValue, exists := attrs[attrKey]
		if !exists || attrValue == nil {
			continue
		}

		ldKey := prop.SchemaOrgProperty

		// Handle sameAs specially — accumulate into an array
		if ldKey == "sameAs" {
			if strVal, ok := attrValue.(string); ok && strVal != "" {
				existing, _ := jsonLD["sameAs"].([]string)
				jsonLD["sameAs"] = append(existing, strVal)
			}
			continue
		}

		// Simple scalar mapping
		if strVal, ok := attrValue.(string); ok && strVal != "" {
			jsonLD[ldKey] = strVal
		} else if numVal, ok := attrValue.(float64); ok {
			jsonLD[ldKey] = numVal
		}
	}
}

// mapObjectProperty maps a JSON object attribute to a nested JSON-LD object.
func mapObjectProperty(jsonLD map[string]any, ldKey string, attrValue any, prop *SchemaProperty) {
	objMap, ok := attrValue.(map[string]any)
	if !ok || len(objMap) == 0 {
		return
	}

	// If the property has its own x-schema-org-type, build a typed sub-object
	if prop.SchemaOrgType != "" {
		subObj := map[string]any{
			"@type": prop.SchemaOrgType,
		}
		if prop.Properties != nil {
			mapPropertiesToJSONLD(subObj, objMap, prop.Properties, prop.SchemaOrgType)
		}
		// Only include if there's more than just @type
		if len(subObj) > 1 {
			jsonLD[ldKey] = subObj
		}
	} else {
		// Plain object without schema.org type — map nested properties directly
		subObj := map[string]any{}
		if prop.Properties != nil {
			mapPropertiesToJSONLD(subObj, objMap, prop.Properties, "")
		}
		if len(subObj) > 0 {
			jsonLD[ldKey] = subObj
		}
	}
}

// mapArrayProperty maps a JSON array attribute to a JSON-LD array.
func mapArrayProperty(jsonLD map[string]any, ldKey string, attrValue any, prop *SchemaProperty, parentType string) {
	arr, ok := attrValue.([]any)
	if !ok || len(arr) == 0 {
		return
	}

	items := prop.Items
	if items == nil {
		// Simple string array — flatten to JSON-LD
		strArr := extractStringArray(arr)
		if len(strArr) > 0 {
			jsonLD[ldKey] = strArr
		}
		return
	}

	// Special handling for FAQ mainEntity — Question with acceptedAnswer
	if parentType == "FAQPage" && ldKey == "mainEntity" && items.SchemaOrgType == "Question" {
		faqEntities := buildFAQMainEntity(arr, items)
		if len(faqEntities) > 0 {
			jsonLD[ldKey] = faqEntities
		}
		return
	}

	// If items are typed objects, build an array of typed objects
	if items.SchemaOrgType != "" && items.Type == "object" {
		var typedArr []map[string]any
		for _, item := range arr {
			if itemMap, ok := item.(map[string]any); ok {
				subObj := map[string]any{
					"@type": items.SchemaOrgType,
				}
				if items.Properties != nil {
					mapPropertiesToJSONLD(subObj, itemMap, items.Properties, items.SchemaOrgType)
				}
				if len(subObj) > 1 {
					typedArr = append(typedArr, subObj)
				}
			}
		}
		if len(typedArr) > 0 {
			jsonLD[ldKey] = typedArr
		}
		return
	}

	// Simple string array
	if items.Type == "string" {
		strArr := extractStringArray(arr)
		if len(strArr) > 0 {
			jsonLD[ldKey] = strArr
		}
		return
	}

	// Untyped object array — map properties if available
	if items.Type == "object" && items.Properties != nil {
		var objArr []map[string]any
		for _, item := range arr {
			if itemMap, ok := item.(map[string]any); ok {
				subObj := map[string]any{}
				mapPropertiesToJSONLD(subObj, itemMap, items.Properties, "")
				if len(subObj) > 0 {
					objArr = append(objArr, subObj)
				}
			}
		}
		if len(objArr) > 0 {
			jsonLD[ldKey] = objArr
		}
	}
}

// mapScalarProperty maps a scalar attribute value to a JSON-LD key.
func mapScalarProperty(jsonLD map[string]any, ldKey string, attrValue any, prop *SchemaProperty) {
	// Handle reviewRating specially — wrap integer in Rating object
	if ldKey == "reviewRating" {
		if rating, ok := attrValue.(float64); ok && rating > 0 {
			jsonLD[ldKey] = map[string]any{
				"@type":       "Rating",
				"ratingValue": int(rating),
				"bestRating":  5,
			}
		}
		return
	}

	switch v := attrValue.(type) {
	case string:
		if v != "" {
			jsonLD[ldKey] = v
		}
	case float64:
		jsonLD[ldKey] = v
	case bool:
		jsonLD[ldKey] = v
	default:
		jsonLD[ldKey] = v
	}
}

// buildFAQMainEntity constructs the FAQ mainEntity array with Question/Answer pairs.
func buildFAQMainEntity(arr []any, itemSchema *SchemaProperty) []map[string]any {
	var entities []map[string]any

	for _, item := range arr {
		qMap, ok := item.(map[string]any)
		if !ok {
			continue
		}

		// Extract question and answer from the data
		var questionText, answerText string

		if itemSchema.Properties != nil {
			for key, prop := range itemSchema.Properties {
				val, exists := qMap[key]
				if !exists || val == nil {
					continue
				}
				strVal, ok := val.(string)
				if !ok || strVal == "" {
					continue
				}
				if prop.SchemaOrgProperty == "name" {
					questionText = strVal
				}
				if prop.SchemaOrgProperty == "acceptedAnswer" {
					answerText = strVal
				}
			}
		}

		if questionText != "" && answerText != "" {
			entities = append(entities, map[string]any{
				"@type": "Question",
				"name":  questionText,
				"acceptedAnswer": map[string]any{
					"@type": "Answer",
					"text":  answerText,
				},
			})
		}
	}

	return entities
}

// BuildEntityURLForJSONLD constructs the canonical URL for an entity.
// Exported so callers in other packages can resolve internal-source
// citation URLs when building JSON-LD payloads.
func BuildEntityURLForJSONLD(ent *Entity, baseURL string) string {
	return baseURL + currentURLSnapshot().EntityPath(ent, nil)
}

// ApplyCanonicalToJSONLD makes the entity's own node agree with the page's
// canonical URL: url becomes canonical, "@id" keeps its fragment (or defaults
// to the lower-cased @type) on top of canonical, and nested references to the
// previously emitted url are rewritten. Organization/company schemas keep
// their own url (the "website" attribute), and @graph documents are skipped.
func ApplyCanonicalToJSONLD(doc map[string]any, schemaSlug, canonical string) {
	if doc == nil || canonical == "" || doc["@graph"] != nil {
		return
	}
	if schemaSlug == "organization" || schemaSlug == "company" {
		return
	}
	old, _ := doc["url"].(string)
	if old != "" && old != canonical {
		for k, v := range doc {
			if k != "url" {
				doc[k] = rewriteJSONLDURLPrefix(v, old, canonical)
			}
		}
	}
	doc["url"] = canonical
	if id, _ := doc["@id"].(string); !strings.HasPrefix(id, canonical) {
		typ, _ := doc["@type"].(string)
		doc["@id"] = canonical + "#" + strings.ToLower(typ)
	}
}

func rewriteJSONLDURLPrefix(v any, old, canonical string) any {
	switch t := v.(type) {
	case string:
		if t == old || strings.HasPrefix(t, old+"#") {
			return canonical + strings.TrimPrefix(t, old)
		}
	case map[string]any:
		for k, e := range t {
			t[k] = rewriteJSONLDURLPrefix(e, old, canonical)
		}
	case []any:
		for i, e := range t {
			t[i] = rewriteJSONLDURLPrefix(e, old, canonical)
		}
	}
	return v
}

// SchemaOrgTypeForEntity returns the Schema.org @type that best
// matches an entity's schema slug, used when emitting internal-source
// citation blocks. Falls back to "CreativeWork" for unknown slugs.
func SchemaOrgTypeForEntity(ent *Entity) string {
	if ent == nil || ent.Schema == nil {
		return "CreativeWork"
	}
	switch ent.Schema.Slug {
	case "blog", "post", "article", "blog_post":
		return "Article"
	case "page":
		return "WebPage"
	default:
		return "CreativeWork"
	}
}

// buildEntityURLForJSONLD is a deprecated alias kept temporarily for
// in-package callers. Once they migrate, this can be deleted.
func buildEntityURLForJSONLD(ent *Entity, baseURL string) string {
	return BuildEntityURLForJSONLD(ent, baseURL)
}

// extractStringArray converts an []interface{} to []string, skipping non-strings.
func extractStringArray(arr []any) []string {
	var result []string
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			if v != "" {
				result = append(result, v)
			}
		case map[string]any:
			// Handle objects with a "name" field
			if name, ok := v["name"].(string); ok && name != "" {
				result = append(result, name)
			}
		}
	}
	return result
}

// hasSchemaOrgAnnotations checks if a schema definition contains x-schema-org-type.
func hasSchemaOrgAnnotations(schemaJSON json.RawMessage) bool {
	// Quick string check before full parse
	return strings.Contains(string(schemaJSON), "x-schema-org-type")
}

// FAQItem represents a question-answer pair for FAQ JSON-LD generation.
type FAQItem struct {
	Question string
	Answer   string
}

// GenerateJSONLDWithFAQ produces JSON-LD, and if FAQ items are provided,
// wraps the result in a @graph array that includes both the entity's own
// JSON-LD and a FAQPage object.
func GenerateJSONLDWithFAQ(ent *Entity, baseURL string, faqItems []FAQItem) (map[string]any, error) {
	// Generate the base JSON-LD
	baseJSONLD, err := GenerateJSONLD(ent, baseURL)
	if err != nil {
		return nil, err
	}

	if len(faqItems) == 0 {
		return baseJSONLD, nil
	}

	// If the entity is already a FAQPage, do not add a second one
	if entityType, ok := baseJSONLD["@type"].(string); ok && entityType == "FAQPage" {
		return baseJSONLD, nil
	}

	// Build FAQPage JSON-LD
	mainEntity := make([]map[string]any, 0, len(faqItems))
	for _, item := range faqItems {
		mainEntity = append(mainEntity, map[string]any{
			"@type": "Question",
			"name":  item.Question,
			"acceptedAnswer": map[string]any{
				"@type": "Answer",
				"text":  item.Answer,
			},
		})
	}

	faqPageJSONLD := map[string]any{
		"@type":      "FAQPage",
		"mainEntity": mainEntity,
	}

	// Remove @context from both items (goes at top level)
	delete(baseJSONLD, "@context")

	// Wrap in @graph
	graphJSONLD := map[string]any{
		"@context": "https://schema.org",
		"@graph": []map[string]any{
			baseJSONLD,
			faqPageJSONLD,
		},
	}

	return graphJSONLD, nil
}

// EditorialProvenance represents the editorial metadata stored on an entity.
type EditorialProvenance struct {
	ReviewedBy     string   `json:"reviewed_by,omitempty"`
	ReviewedAt     string   `json:"reviewed_at,omitempty"`
	FactChecked    bool     `json:"fact_checked,omitempty"`
	Sources        []string `json:"sources,omitempty"`
	LastVerifiedAt string   `json:"last_verified_at,omitempty"`
}

// EnrichJSONLDWithEditorial adds editorial provenance data to JSON-LD output.
// It reads the entity's Editorial field and adds editor and citation properties.
func EnrichJSONLDWithEditorial(jsonLD map[string]any, editorial json.RawMessage) {
	if len(editorial) == 0 || string(editorial) == "{}" || string(editorial) == "null" {
		return
	}

	var prov EditorialProvenance
	if err := json.Unmarshal(editorial, &prov); err != nil {
		return
	}

	// Add editor if reviewed_by is present
	if prov.ReviewedBy != "" {
		jsonLD["editor"] = map[string]any{
			"@type": "Person",
			"name":  prov.ReviewedBy,
		}
	}

	// Add citations from sources
	if len(prov.Sources) > 0 {
		citations := make([]map[string]any, 0, len(prov.Sources))
		for _, source := range prov.Sources {
			citations = append(citations, map[string]any{
				"@type": "CreativeWork",
				"url":   source,
			})
		}
		jsonLD["citation"] = citations
	}

	// Add lastReviewed date
	if prov.LastVerifiedAt != "" {
		jsonLD["lastReviewed"] = prov.LastVerifiedAt
	} else if prov.ReviewedAt != "" {
		jsonLD["lastReviewed"] = prov.ReviewedAt
	}
}

// EvidenceLinkForJSONLD is a lightweight struct for passing evidence data into the
// JSON-LD enrichment function, avoiding a circular import of the evidence package.
type EvidenceLinkForJSONLD struct {
	FieldPath       string
	LinkType        string
	ConfidenceScore int
	VerifiedAt      *string // ISO 8601 timestamp, nil if not verified
	SourceTitle     string
	SourceType      string
	// SourceURL is the URL used in citation blocks. For external_url sources
	// this is the user-supplied URL; for internal entity-backed sources the
	// server layer resolves the entity's canonical URL before passing in.
	SourceURL string
	// SourceURLType is the Schema.org @type of the citation block. Defaults
	// to "CreativeWork" when empty. Server layer sets this to "Article" /
	// "Person" / etc. for internal entity sources so LLMs and crawlers
	// see a specific type instead of the generic fallback.
	SourceURLType string
}

// EnrichJSONLDWithEvidence adds claim/evidence annotations to JSON-LD output.
// It adds:
//   - "citation" entries (Schema.org CreativeWork) for every external evidence URL
//   - "mentions" entries (Schema.org Claim) for every field with evidence,
//     each carrying its own per-field "citation" subset
//   - "evidenceCount" and "evidenceLevel" aggregate signals (non-standard but
//     readable by LLM crawlers)
//   - "lastReviewed" timestamp from the most recent verification
//
// Note: this does not emit ClaimReview rich-result markup — that requires
// the page to be a dedicated fact-check page. Plain Claim under mentions
// is the maximally compatible structure that survives audit by quality
// raters and gets picked up by AI Overview citation pipelines.
func EnrichJSONLDWithEvidence(jsonLD map[string]any, links []EvidenceLinkForJSONLD) {
	if len(links) == 0 {
		return
	}

	// Collect unique citation URLs from evidence sources
	var citations []map[string]any
	seenURLs := make(map[string]bool)
	var latestVerified string

	// Group links by field path for claim annotations
	fieldClaims := make(map[string][]EvidenceLinkForJSONLD)
	for _, link := range links {
		fieldClaims[link.FieldPath] = append(fieldClaims[link.FieldPath], link)

		// Collect unique citation URLs
		if link.SourceURL != "" && !seenURLs[link.SourceURL] {
			seenURLs[link.SourceURL] = true
			urlType := link.SourceURLType
			if urlType == "" {
				urlType = "CreativeWork"
			}
			citation := map[string]any{
				"@type": urlType,
				"name":  link.SourceTitle,
				"url":   link.SourceURL,
			}
			citations = append(citations, citation)
		}

		// Track the most recent verification date
		if link.VerifiedAt != nil && *link.VerifiedAt > latestVerified {
			latestVerified = *link.VerifiedAt
		}
	}

	// Add citations (merge with any existing editorial citations)
	if len(citations) > 0 {
		existing, hasExisting := jsonLD["citation"]
		if hasExisting {
			if existingArr, ok := existing.([]map[string]any); ok {
				citations = append(existingArr, citations...)
			}
		}
		jsonLD["citation"] = citations
	}

	// Add lastReviewed from evidence verification (only if no editorial date already set)
	if latestVerified != "" {
		if _, hasReviewed := jsonLD["lastReviewed"]; !hasReviewed {
			jsonLD["lastReviewed"] = latestVerified
		}
	}

	// Add evidence level annotation as a custom property
	// This is a non-standard but useful signal for LLMs
	totalConfidence := 0
	for _, link := range links {
		totalConfidence += link.ConfidenceScore
	}
	avgConfidence := totalConfidence / len(links)

	var evidenceLevel string
	switch {
	case avgConfidence >= 80:
		evidenceLevel = "strong"
	case avgConfidence >= 50:
		evidenceLevel = "moderate"
	default:
		evidenceLevel = "weak"
	}

	jsonLD["evidenceLevel"] = evidenceLevel
	jsonLD["evidenceCount"] = len(links)

	// Per-field Schema.org Claim annotations under "mentions". One Claim
	// per field that has at least one evidence link, with the per-field
	// citation subset attached directly. Stable ordering so successive
	// renders produce byte-identical JSON-LD.
	if len(fieldClaims) > 0 {
		entityIRI := extractEntityIRI(jsonLD)

		fieldPaths := make([]string, 0, len(fieldClaims))
		for fp := range fieldClaims {
			fieldPaths = append(fieldPaths, fp)
		}
		sort.Strings(fieldPaths)

		claims := make([]map[string]any, 0, len(fieldPaths))
		for _, fp := range fieldPaths {
			if claim := buildFieldClaim(entityIRI, fp, fieldClaims[fp]); claim != nil {
				claims = append(claims, claim)
			}
		}

		if len(claims) > 0 {
			if existing, ok := jsonLD["mentions"].([]map[string]any); ok {
				claims = append(existing, claims...)
			}
			jsonLD["mentions"] = claims
		}
	}
}

// extractEntityIRI pulls a stable identifier from an existing JSON-LD blob.
// Prefers @id, falls back to url. Empty string when neither is set — claims
// will then be emitted without an @id or appearance.
func extractEntityIRI(jsonLD map[string]any) string {
	if id, ok := jsonLD["@id"].(string); ok && id != "" {
		return id
	}
	if url, ok := jsonLD["url"].(string); ok && url != "" {
		return url
	}
	return ""
}

// buildFieldClaim produces a Schema.org Claim object for a single fact field.
// Per-field citations are the subset of evidence sources tied to this field
// that have an external URL; internal-only evidence (case studies, credentials
// without URLs) is reflected in evidenceCount but not in citation arrays.
func buildFieldClaim(entityIRI, fieldPath string, links []EvidenceLinkForJSONLD) map[string]any {
	if fieldPath == "" {
		return nil
	}
	claim := map[string]any{
		"@type": "Claim",
		"name":  humaniseFieldPath(fieldPath),
	}
	if entityIRI != "" {
		// If the entity IRI already carries a fragment (e.g. "...#article"),
		// extend it with a hyphen so the resulting @id stays a valid IRI
		// with a single fragment.
		sep := "#claim-"
		if strings.Contains(entityIRI, "#") {
			sep = "-claim-"
		}
		claim["@id"] = entityIRI + sep + sanitiseFragment(fieldPath)
		claim["appearance"] = map[string]any{"@id": entityIRI}
	}

	var perFieldCitations []map[string]any
	seenURLs := make(map[string]bool)
	for _, link := range links {
		if link.SourceURL == "" || seenURLs[link.SourceURL] {
			continue
		}
		seenURLs[link.SourceURL] = true
		urlType := link.SourceURLType
		if urlType == "" {
			urlType = "CreativeWork"
		}
		perFieldCitations = append(perFieldCitations, map[string]any{
			"@type": urlType,
			"name":  link.SourceTitle,
			"url":   link.SourceURL,
		})
	}
	if len(perFieldCitations) > 0 {
		claim["citation"] = perFieldCitations
	}
	return claim
}

// humaniseFieldPath turns "years_experience" into "Years Experience" and
// "seo.meta_title" into "Seo Meta Title". Dot-separated paths are
// flattened — nested field labels aren't worth the complexity.
func humaniseFieldPath(fp string) string {
	s := strings.ReplaceAll(fp, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	parts := strings.Fields(s)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// sanitiseFragment makes a value safe for use as a URL fragment.
// Strict allow-list (alphanumeric, '.', '-', '_'); everything else
// collapses to '-' so dotted/array paths still produce a readable id.
func sanitiseFragment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
