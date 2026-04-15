// Package ruleset parses and validates AWS GameLift FlexMatch rule set JSON
// documents (the same payload accepted by CreateMatchmakingRuleSet).
package ruleset

import (
	"encoding/json"
	"errors"
	"fmt"
)

// RuleSet is the parsed top-level FlexMatch rule set.
type RuleSet struct {
	Name                string             `json:"name"`
	RuleLanguageVersion string             `json:"ruleLanguageVersion"`
	PlayerAttributes    []PlayerAttribute  `json:"playerAttributes,omitempty"`
	Algorithm           Algorithm          `json:"algorithm"`
	Teams               []Team             `json:"teams"`
	Rules               []Rule             `json:"rules,omitempty"`
	Expansions          []Expansion        `json:"expansions,omitempty"`

	// AcceptanceRequired, when true, holds matches formed by the engine in
	// REQUIRES_ACCEPTANCE state until every player on every matched ticket
	// has accepted via Matchmaker.Accept. Mirrors the FlexMatch field of the
	// same name on MatchmakingConfiguration.
	AcceptanceRequired bool `json:"acceptanceRequired,omitempty"`

	// AcceptanceTimeoutSeconds bounds how long a proposed match may sit in
	// REQUIRES_ACCEPTANCE. When the deadline passes without full acceptance,
	// the proposal is discarded and involved tickets move to TIMED_OUT.
	// Zero means no timeout. Ignored when AcceptanceRequired is false.
	AcceptanceTimeoutSeconds int `json:"acceptanceTimeoutSeconds,omitempty"`
}

// PlayerAttribute declares a player attribute the rule set will reference.
type PlayerAttribute struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Default json.RawMessage `json:"default,omitempty"`
}

// Algorithm captures the rule set's algorithm block.
type Algorithm struct {
	Strategy           string `json:"strategy,omitempty"`
	BatchingPreference string `json:"batchingPreference,omitempty"`
	BalancedAttribute  string `json:"balancedAttribute,omitempty"`
	BackfillPriority   string `json:"backfillPriority,omitempty"`
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

	// distance
	MaxDistance *float64 `json:"maxDistance,omitempty"`
	MinDistance *float64 `json:"minDistance,omitempty"`

	// absoluteSort
	SortDirection string `json:"sortDirection,omitempty"`
	SortAttribute string `json:"sortAttribute,omitempty"`
	MapKey        string `json:"mapKey,omitempty"`
	SortReference string `json:"sortReference,omitempty"`

	// batchDistance
	BatchAttribute       string   `json:"batchAttribute,omitempty"`
	MaxAttributeDistance *float64 `json:"maxAttributeDistance,omitempty"`
	PartyAggregation     string   `json:"partyAggregation,omitempty"`

	// collection
	MinCount *int `json:"minCount,omitempty"`
	MaxCount *int `json:"maxCount,omitempty"`

	// latency
	MaxLatency *int `json:"maxLatency,omitempty"`

	// compound
	Statement *CompoundStatement `json:"statement,omitempty"`
}

// CompoundStatement is the body of a compound rule: a condition combinator
// applied to a list of child rule names.
type CompoundStatement struct {
	Condition string   `json:"condition"`
	Rules     []string `json:"rules"`
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
