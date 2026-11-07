package assist

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Redact removes inline env and Helm values before evidence reaches a model.
// Arbitrary logs can contain secrets in other forms and need separate care.
func Redact(input string) string {
	var value any
	if json.Unmarshal([]byte(input), &value) == nil {
		b, err := json.Marshal(redactValue(value, false))
		if err == nil {
			return string(b)
		}
	}
	return redactText(input)
}

func redactValue(value any, helm bool) any {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			key := strings.ToLower(strings.ReplaceAll(k, "_", ""))
			switch key {
			case "env", "envvars", "environment", "parameters", "valuesobject", "secretkey", "publickey":
				v[k] = "[redacted]"
			case "values":
				if helm {
					v[k] = "[redacted]"
				} else {
					v[k] = redactValue(item, false)
				}
			case "image":
				image, ok := item.(string)
				if ok && regexp.MustCompile(`^(?:[a-z0-9.-]+(?::[0-9]+)?/)?[a-z0-9]+(?:[._/-][a-z0-9]+)*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(?:@sha256:[a-f0-9]{64})?$`).MatchString(image) {
					continue
				}
				v[k] = redactValue(item, helm)
			default:
				v[k] = redactValue(item, helm || key == "helm")
			}
		}
		return v
	case []any:
		for i := range v {
			v[i] = redactValue(v[i], helm)
		}
		return v
	case string:
		var nested any
		if json.Unmarshal([]byte(v), &nested) == nil {
			b, err := json.Marshal(redactValue(nested, false))
			if err == nil {
				return string(b)
			}
		}
		return redactText(v)
	default:
		return value
	}
}

func redactText(input string) string {
	block := regexp.MustCompile(`(?im)^\s*(?:env|envvars|environment|parameters|valuesobject)\s*:`)
	if loc := block.FindStringIndex(input); loc != nil {
		input = input[:loc[0]] + "[redacted configuration]"
	}
	assign := regexp.MustCompile(`(?im)(^|[\s,;])([a-z_][a-z0-9_.-]*[ \t]*[=:][ \t]*).*$`)
	return assign.ReplaceAllString(input, "${1}${2}[redacted]")
}
