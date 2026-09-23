package expr

import "fmt"

// Checks functions, team references, and declared attribute access.
func Validate(n Node, teams map[string]struct{}, attributes map[string]string) error {
	switch v := n.(type) {
	case FuncCall:
		switch v.Name {
		case "flatten", "count", "set_intersection", "avg", "sum", "min", "max", "median", "stddev":
		default:
			return fmt.Errorf("unknown function %q", v.Name)
		}
		if err := Validate(v.Arg, teams, attributes); err != nil {
			return err
		}
		switch v.Name {
		case "avg", "sum", "min", "max", "median", "stddev":
			if kind := knownKind(v.Arg, attributes); kind != "" && kind != "number" {
				return fmt.Errorf("function %q requires numeric values, got %s", v.Name, kind)
			}
		}
	case PlayerAccess:
		for _, name := range v.Teams {
			if _, ok := teams[name]; !ok {
				return fmt.Errorf("unknown team %q", name)
			}
		}
		if v.HasIndex && attributes[v.Attr] != "" && attributes[v.Attr] != "string_number_map" {
			return fmt.Errorf("attribute %q is not a string_number_map", v.Attr)
		}
	}
	return nil
}

func knownKind(n Node, attrs map[string]string) string {
	switch v := n.(type) {
	case NumberLit:
		return "number"
	case StringLit:
		return "string"
	case PlayerAccess:
		if v.Attr == "" {
			return "string"
		}
		if v.HasIndex {
			return "number"
		}
		return attrs[v.Attr]
	case FuncCall:
		if v.Name == "count" || v.Name == "avg" || v.Name == "sum" || v.Name == "min" || v.Name == "max" || v.Name == "median" || v.Name == "stddev" {
			return "number"
		}
		if v.Name == "flatten" {
			return knownKind(v.Arg, attrs)
		}
	}
	return ""
}
