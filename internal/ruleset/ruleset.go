// Package ruleset parses and validates AWS GameLift FlexMatch rule set JSON
// documents (the same payload accepted by CreateMatchmakingRuleSet).
package ruleset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// RuleSet is the parsed top-level FlexMatch rule set.
type RuleSet struct {
	Name                string            `json:"name"`
	RuleLanguageVersion string            `json:"ruleLanguageVersion"`
	PlayerAttributes    []PlayerAttribute `json:"playerAttributes,omitempty"`
	Algorithm           Algorithm         `json:"algorithm"`
	Teams               []Team            `json:"teams"`
	Rules               []Rule            `json:"rules,omitempty"`
	Expansions          []Expansion       `json:"expansions,omitempty"`

	// AcceptanceRequired, when true, holds matches formed by the engine in
	// REQUIRES_ACCEPTANCE state until every player on every matched ticket
	// has accepted via Matchmaker.Accept. Mirrors the FlexMatch field of the
	// same name on MatchmakingConfiguration.
	AcceptanceRequired bool `json:"acceptanceRequired,omitempty"`

	// AcceptanceTimeoutSeconds bounds how long a proposed match may sit in
	// REQUIRES_ACCEPTANCE. When the deadline passes without full acceptance,
	// the proposal is discarded: tickets whose every player had accepted return
	// to SEARCHING, and the rest move to CANCELLED (matching FlexMatch, which
	// cancels tickets that reject or fail to respond to a proposed match).
	// Zero means no timeout. Ignored when AcceptanceRequired is false.
	AcceptanceTimeoutSeconds int `json:"acceptanceTimeoutSeconds,omitempty"`

	// RequestTimeoutSeconds bounds how long a matchmaking request (ticket) may
	// stay in matchmaking before it fails. When a queued or re-queued
	// (SEARCHING) ticket has waited this long, measured from its original
	// enqueue time, it moves to TIMED_OUT. Zero means no timeout. Mirrors the
	// FlexMatch field of the same name on MatchmakingConfiguration; unlike
	// AcceptanceTimeoutSeconds it applies regardless of AcceptanceRequired.
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds,omitempty"`
}

// PlayerAttribute declares a player attribute the rule set will reference.
type PlayerAttribute struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Default json.RawMessage `json:"default,omitempty"`
}

// Algorithm captures the rule set's algorithm block.
type Algorithm struct {
	Strategy              string   `json:"strategy,omitempty"`
	BatchingPreference    string   `json:"batchingPreference,omitempty"`
	BalancedAttribute     string   `json:"balancedAttribute,omitempty"`
	SortByAttributes      []string `json:"sortByAttributes,omitempty"`
	BackfillPriority      string   `json:"backfillPriority,omitempty"`
	ExpansionAgeSelection string   `json:"expansionAgeSelection,omitempty"`
}

// Team describes one team slot. Quantity > 1 means the team is created
// multiple times in a single match.
type Team struct {
	Name       string `json:"name"`
	MinPlayers int    `json:"minPlayers"`
	MaxPlayers int    `json:"maxPlayers"`
	Quantity   int    `json:"quantity,omitempty"`
}

// RuleType enumerates the FlexMatch rule kinds.
type RuleType string

const (
	RuleComparison    RuleType = "comparison"
	RuleDistance      RuleType = "distance"
	RuleAbsoluteSort  RuleType = "absoluteSort"
	RuleDistanceSort  RuleType = "distanceSort"
	RuleBatchDistance RuleType = "batchDistance"
	RuleCollection    RuleType = "collection"
	RuleLatency       RuleType = "latency"
	RuleCompound      RuleType = "compound"
)

// Rule is a single rule entry. Fields not relevant to the rule's Type are zero.
// referenceValue may be a literal number/string OR a property expression
// string, hence kept as RawMessage to preserve fidelity until evaluation.
type Rule struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Type        RuleType `json:"type"`

	// comparison / distance / collection
	Measurements   []string        `json:"measurements,omitempty"`
	ReferenceValue json.RawMessage `json:"referenceValue,omitempty"`

	// comparison
	Operation string `json:"operation,omitempty"`

	// distance / batchDistance / latency
	MaxDistance *float64 `json:"maxDistance,omitempty"`
	MinDistance *float64 `json:"minDistance,omitempty"`

	// absoluteSort / distanceSort
	SortDirection string `json:"sortDirection,omitempty"`
	SortAttribute string `json:"sortAttribute,omitempty"`
	MapKey        string `json:"mapKey,omitempty"` // minValue | maxValue for map attrs

	// batchDistance
	BatchAttribute string `json:"batchAttribute,omitempty"`

	// partyAggregation applies to most rule types: min | max | avg (default
	// avg), or union | intersection for collection rules (default union).
	PartyAggregation string `json:"partyAggregation,omitempty"`

	// collection
	MinCount *int `json:"minCount,omitempty"`
	MaxCount *int `json:"maxCount,omitempty"`

	// latency
	MaxLatency        *int   `json:"maxLatency,omitempty"`
	DistanceReference string `json:"distanceReference,omitempty"` // min | avg

	// compound: a logical statement string, e.g. "or(and(A,B), not(C))".
	Statement string `json:"statement,omitempty"`
}

// UnmarshalJSON decodes a Rule, additionally accepting string-encoded numbers
// for maxDistance/minDistance. The FlexMatch documentation is internally
// inconsistent about these fields — the schema page shows them as numbers while
// the rule-type examples show quoted strings (e.g. "maxDistance":"500") — so we
// accept both forms.
func (r *Rule) UnmarshalJSON(b []byte) error {
	type alias Rule
	aux := &struct {
		MaxDistance json.RawMessage `json:"maxDistance,omitempty"`
		MinDistance json.RawMessage `json:"minDistance,omitempty"`
		*alias
	}{alias: (*alias)(r)}
	if err := json.Unmarshal(b, aux); err != nil {
		return err
	}
	if len(aux.MaxDistance) > 0 {
		f, err := ParseNumber(aux.MaxDistance)
		if err != nil {
			return fmt.Errorf("maxDistance: %w", err)
		}
		r.MaxDistance = &f
	}
	if len(aux.MinDistance) > 0 {
		f, err := ParseNumber(aux.MinDistance)
		if err != nil {
			return fmt.Errorf("minDistance: %w", err)
		}
		r.MinDistance = &f
	}
	return nil
}

// ParseNumber decodes a JSON number, additionally accepting a string-encoded
// number such as "500". A JSON null or empty input yields an error.
func ParseNumber(raw []byte) (float64, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid numeric string %q", s)
		}
		return f, nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	return f, nil
}

// Expansion declares a time-driven loosening of a target value.
type Expansion struct {
	Target string          `json:"target"`
	Steps  []ExpansionStep `json:"steps"`
}

// ExpansionStep applies Value once WaitTimeSeconds have elapsed.
type ExpansionStep struct {
	WaitTimeSeconds int             `json:"waitTimeSeconds"`
	Value           json.RawMessage `json:"value"`
}

// ErrInvalidRuleSet is returned when the rule set JSON is malformed or fails
// semantic validation.
var ErrInvalidRuleSet = errors.New("flexi: invalid rule set")

// Parse parses a FlexMatch rule set JSON document and validates it.
func Parse(body []byte) (*RuleSet, error) {
	var rs RuleSet
	if err := json.Unmarshal(body, &rs); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInvalidRuleSet, err)
	}
	if err := rs.Validate(); err != nil {
		return nil, err
	}
	return &rs, nil
}
