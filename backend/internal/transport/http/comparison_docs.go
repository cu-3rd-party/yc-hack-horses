package http

func schemaRef(name string) schemaNode        { return schemaNode{"$ref": "#/components/schemas/" + name} }
func schemaArray(items schemaNode) schemaNode { return schemaNode{"type": "array", "items": items} }

func addComparisonSchemas(s map[string]schemaNode) {
	id := schemaNode{"type": "string", "format": "uuid"}
	sha := schemaNode{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	point := schemaNode{"type": "array", "minItems": 2, "maxItems": 2, "items": schemaNode{"type": "number", "minimum": 0, "maximum": 1}}
	polygon := schemaNode{"type": "array", "minItems": 3, "items": point}
	traitPolygon := schemaNode{"type": "array", "minItems": 3, "items": point, "nullable": true}
	obj := func() schemaNode { return schemaNode{"type": "object"} }
	label := strSchema("")
	s["ComparisonTrait"] = obj().prop("key", label).prop("value", label).prop("polygon", traitPolygon).required("key", "value", "polygon")
	s["HorseAnnotation"] = obj().prop("horse_polygon", polygon).prop("traits", schemaArray(schemaRef("ComparisonTrait"))).required("horse_polygon", "traits")
	s["HorseCandidate"] = obj().prop("horse_id", id).prop("name", label).prop("best_photo_id", id).
		prop("cosine_similarity", schemaNode{"type": "number", "minimum": -1, "maximum": 1}).required("horse_id", "name", "best_photo_id", "cosine_similarity")
	s["HorseDetection"] = obj().prop("detection_id", id).prop("detection_confidence", schemaNode{"type": "number", "minimum": 0, "maximum": 1}).
		prop("annotation", schemaRef("HorseAnnotation")).prop("candidates", schemaArray(schemaRef("HorseCandidate"))).required("detection_id", "detection_confidence", "annotation", "candidates")
	s["ComparisonRequest"] = obj().prop("blob_sha256", sha).prop("top_k", schemaNode{"type": "integer", "minimum": 1, "default": 5}).required("blob_sha256")
	s["ComparisonResult"] = obj().prop("comparison_id", id).prop("blob_sha256", sha).prop("detections", schemaArray(schemaRef("HorseDetection"))).required("comparison_id", "blob_sha256", "detections")
	decision := func(action, field string, value schemaNode) schemaNode {
		return obj().prop("action", schemaNode{"type": "string", "enum": []string{action}}).prop(field, value).prop("observation_name", label).prop("comment", strSchema("Комментарий к наблюдению, опционально")).required("action", field, "observation_name")
	}
	s["ComparisonDecision"] = schemaNode{"oneOf": []schemaNode{decision("match", "horse_id", id), decision("create_horse", "horse_name", label)}}
	s["DecisionResult"] = obj().prop("observation_id", id).prop("horse_id", id).prop("remaining_detection_ids", schemaArray(id)).required("observation_id", "horse_id", "remaining_detection_ids")
	s["CancelResult"] = obj().prop("status", schemaNode{"type": "string", "enum": []string{"cancelled"}}).prop("saved_observation_ids", schemaArray(id)).prop("skipped_detection_ids", schemaArray(id)).required("status", "saved_observation_ids", "skipped_detection_ids")
	nullableAnnotation := schemaNode{"oneOf": []schemaNode{schemaRef("HorseAnnotation"), {"type": "object", "nullable": true, "enum": []any{nil}}}}
	s["ObservationPhoto"] = obj().prop("photo_id", id).prop("blob_sha256", sha).prop("name", label).prop("annotation", nullableAnnotation).required("photo_id", "blob_sha256", "annotation")
}
