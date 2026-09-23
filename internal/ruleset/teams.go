package ruleset

import "strconv"

// Returns the concrete names created by a team declaration.
func ExpandedTeamNames(t Team) []string {
	quantity := t.Quantity
	if quantity <= 0 {
		quantity = 1
	}
	names := make([]string, quantity)
	for i := range names {
		names[i] = t.Name
		if quantity > 1 {
			names[i] += "_" + strconv.Itoa(i+1)
		}
	}
	return names
}
