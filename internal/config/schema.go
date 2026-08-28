package config

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
)

func Schema() ([]byte, error) {
	reflector := &jsonschema.Reflector{
		AllowAdditionalProperties:  false,
		RequiredFromJSONSchemaTags: true,
		ExpandedStruct:             true,
	}
	schema := reflector.Reflect(&Config{})
	schema.ID = jsonschema.ID("https://raw.githubusercontent.com/ssubedir/draincheck/main/schema/draincheck.schema.json")
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return append(data, '\n'), nil
}
