package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/mockwave/mockwave/domain"
	"gopkg.in/yaml.v3"
)

// GeneratedPair is a matched Rule + Simulation generated from one OpenAPI endpoint.
type GeneratedPair struct {
	Rule       domain.Rule
	Simulation domain.Simulation
}

// FetchAndParse retrieves an OpenAPI 2.0 or 3.0 spec from a URL or file path
// and returns it as an openapi3.T (Swagger 2.0 is automatically converted).
func FetchAndParse(source string) (*openapi3.T, error) {
	var data []byte
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(source)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch spec: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("failed to fetch spec: HTTP %d", resp.StatusCode)
		}
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch spec: %w", err)
		}
	} else {
		var err error
		data, err = os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("failed to read spec: %w", err)
		}
	}
	return parseSpec(data)
}

// parseSpec detects the spec version and returns an openapi3.T.
func parseSpec(data []byte) (*openapi3.T, error) {
	// YAML → generic map for version detection and JSON conversion.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid spec: %w", err)
	}
	// gopkg.in/yaml.v3 unmarshals non-string keys (e.g., HTTP status codes) as
	// map[interface{}]interface{}, which json.Marshal cannot handle. Convert
	// recursively to map[string]interface{}.
	convertedRaw := convertYAMLMap(raw)
	converted, _ := convertedRaw.(map[string]interface{})
	jsonData, err := json.Marshal(converted)
	if err != nil {
		return nil, fmt.Errorf("invalid spec: %w", err)
	}

	swagger, _ := converted["swagger"].(string)
	openapiVer, _ := converted["openapi"].(string)

	switch {
	case swagger == "2.0":
		var sw2 openapi2.T
		if err := json.Unmarshal(jsonData, &sw2); err != nil {
			return nil, fmt.Errorf("invalid spec: %w", err)
		}
		doc, err := openapi2conv.ToV3(&sw2)
		if err != nil {
			return nil, fmt.Errorf("invalid spec: %w", err)
		}
		return doc, nil
	case strings.HasPrefix(openapiVer, "3."):
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = true
		doc, err := loader.LoadFromData(jsonData)
		if err != nil {
			return nil, fmt.Errorf("invalid spec: %w", err)
		}
		return doc, nil
	default:
		return nil, fmt.Errorf("unsupported spec version (must be OpenAPI 2.0 or 3.0)")
	}
}

// convertYAMLMap recursively converts map[interface{}]interface{} (produced by
// yaml.v3 when unmarshalling non-string keys like HTTP status codes) into
// map[string]interface{} so encoding/json can marshal it.
func convertYAMLMap(v interface{}) interface{} {
	switch val := v.(type) {
	case map[interface{}]interface{}:
		m := make(map[string]interface{}, len(val))
		for k, v2 := range val {
			m[fmt.Sprintf("%v", k)] = convertYAMLMap(v2)
		}
		return m
	case map[string]interface{}:
		m := make(map[string]interface{}, len(val))
		for k, v2 := range val {
			m[k] = convertYAMLMap(v2)
		}
		return m
	case []interface{}:
		for i, item := range val {
			val[i] = convertYAMLMap(item)
		}
		return val
	default:
		return v
	}
}

var paramRe = regexp.MustCompile(`\{[^}]+\}`)

// makeID converts an HTTP method + path into a slug ID.
// Path parameters are normalised to the literal "id".
// Example: GET /users/{userId}/orders → get-users-id-orders
//
// Note: different path parameters in the same position (e.g. {userId} vs {teamId})
// will produce the same ID.
func makeID(method, path string) string {
	normalized := paramRe.ReplaceAllString(path, "id")
	parts := strings.FieldsFunc(normalized, func(r rune) bool { return r == '/' })
	segments := append([]string{strings.ToLower(method)}, parts...)
	return strings.Join(segments, "-")
}

// toGlob replaces path parameter placeholders with glob wildcards.
// Example: /users/{id} → /users/*
func toGlob(path string) string {
	return paramRe.ReplaceAllString(path, "*")
}

// GeneratePairs generates Rule+Simulation pairs from an OpenAPI 3.0 spec.
//
// pathPrefix, if non-empty, restricts generation to paths that start with it.
// tagsCSV, if non-empty, restricts generation to operations with at least one matching tag.
//
// Returns generated pairs, descriptions of skipped paths (no 2xx response), and any error.
func GeneratePairs(spec *openapi3.T, pathPrefix, tagsCSV string) ([]GeneratedPair, []string, error) {
	if spec.Paths == nil {
		return nil, nil, nil
	}

	tagSet := make(map[string]bool)
	if tagsCSV != "" {
		for _, t := range strings.Split(tagsCSV, ",") {
			tagSet[strings.TrimSpace(t)] = true
		}
	}

	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}

	var pairs []GeneratedPair
	var skipped []string

	for path, item := range spec.Paths.Map() {
		if pathPrefix != "" && !strings.HasPrefix(path, pathPrefix) {
			continue
		}

		ops := map[string]*openapi3.Operation{
			"GET":     item.Get,
			"POST":    item.Post,
			"PUT":     item.Put,
			"PATCH":   item.Patch,
			"DELETE":  item.Delete,
			"HEAD":    item.Head,
			"OPTIONS": item.Options,
		}

		for _, method := range methods {
			op := ops[method]
			if op == nil {
				continue
			}

			if len(tagSet) > 0 {
				matched := false
				for _, tag := range op.Tags {
					if tagSet[tag] {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}

			status, body, ok := findSuccessResponse(op)
			if !ok {
				skipped = append(skipped, fmt.Sprintf("%s %s (no 2xx response defined)", method, path))
				continue
			}

			id := makeID(method, path)
			simID := id + "-sim"

			sim := domain.Simulation{
				ID:       simID,
				Protocol: "http",
				Response: domain.HTTPResponse{
					Status: status,
					Body:   body,
				},
			}
			rule := domain.Rule{
				ID:   id,
				Name: method + " " + path,
				Match: domain.MatchCriteria{
					Method: method,
					Path:   toGlob(path),
				},
				Buckets: []domain.WeightedBucket{
					{Weight: 100, Action: domain.ActionSimulate, SimulationID: simID},
				},
			}
			pairs = append(pairs, GeneratedPair{Rule: rule, Simulation: sim})
		}
	}

	if len(pairs) == 0 && len(skipped) == 0 {
		var parts []string
		if pathPrefix != "" {
			parts = append(parts, "path_prefix="+pathPrefix)
		}
		if tagsCSV != "" {
			parts = append(parts, "tags=["+tagsCSV+"]")
		}
		if len(parts) > 0 {
			return nil, nil, fmt.Errorf("no paths matched %s", strings.Join(parts, " or "))
		}
	}

	return pairs, skipped, nil
}

// findSuccessResponse returns the first 2xx status + response body for an operation.
func findSuccessResponse(op *openapi3.Operation) (int, interface{}, bool) {
	if op.Responses == nil {
		return 0, nil, false
	}
	for _, code := range []string{"200", "201", "202", "203", "204"} {
		ref := op.Responses.Value(code)
		if ref == nil || ref.Value == nil {
			continue
		}
		body := extractBody(ref.Value)
		return atoi(code), body, true
	}
	return 0, nil, false
}

// atoi converts a 3-digit status-code string to int without importing strconv.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// extractBody returns an example or schema-derived body from a response definition.
// Returns nil if no application/json content is defined.
func extractBody(resp *openapi3.Response) interface{} {
	if resp.Content == nil {
		return nil
	}
	mt := resp.Content.Get("application/json")
	if mt == nil {
		return nil
	}
	if mt.Example != nil {
		return mt.Example
	}
	if mt.Schema != nil && mt.Schema.Value != nil {
		return schemaZero(mt.Schema.Value)
	}
	return nil
}

// schemaZero returns typed zero values for a JSON schema.
// allOf/anyOf/oneOf compositions return {} (fallback).
func schemaZero(schema *openapi3.Schema) interface{} {
	if schema == nil {
		return nil
	}
	// schema.Type is *openapi3.Types (pointer to []string) in kin-openapi v0.145.
	kind := ""
	if schema.Type != nil && len(*schema.Type) > 0 {
		kind = (*schema.Type)[0]
	}
	switch kind {
	case "object", "":
		obj := make(map[string]interface{})
		for name, prop := range schema.Properties {
			if prop != nil && prop.Value != nil {
				obj[name] = schemaZero(prop.Value)
			}
		}
		return obj
	case "array":
		return []interface{}{}
	case "string":
		return ""
	case "integer", "number":
		return 0
	case "boolean":
		return false
	default:
		return nil
	}
}
