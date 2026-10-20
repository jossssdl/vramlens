package bench

import "encoding/json"

// DefaultPrompt is the built-in structured-extraction task used when the user
// does not supply a prompt.
const DefaultPrompt = "Extract the person from this text: 'Marta Ruiz is a 34 year old cardiologist who enjoys climbing, chess and cooking.'"

// DefaultSchemaJSON is the schema matching DefaultPrompt.
const DefaultSchemaJSON = `{
  "type": "object",
  "properties": {
    "name": {"type": "string"},
    "age": {"type": "integer", "minimum": 0},
    "profession": {"type": "string"},
    "hobbies": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["name", "age", "profession", "hobbies"],
  "additionalProperties": false
}`

// DefaultSchema returns DefaultSchemaJSON decoded.
func DefaultSchema() map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(DefaultSchemaJSON), &m)
	return m
}
