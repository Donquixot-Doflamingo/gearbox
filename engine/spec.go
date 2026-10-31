// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Go shapes of the definition model (contracts/openapi/gearbox-admin.v1.yaml
// components/schemas). Polymorphic parts (conditions, data slots) stay raw and
// are compiled by their own code.

type schemaRef struct {
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

func (r *schemaRef) String() string { return fmt.Sprintf("%s@%d", r.Name, r.Version) }

type schemaSpec struct {
	JSONSchema json.RawMessage   `json:"jsonSchema"`
	Examples   []json.RawMessage `json:"examples"`
}

type loggingSpec struct {
	Sample     *float64 `json:"sample"`
	Fields     []string `json:"fields"`
	AIPayloads bool     `json:"aiPayloads"`
}

type decisionSpec struct {
	Entry       string            `json:"entry"`
	InputSchema *schemaRef        `json:"inputSchema"`
	Nodes       []json.RawMessage `json:"nodes"`
	Output      *struct {
		Schema *schemaRef `json:"schema"`
	} `json:"output"`
	Fallback     json.RawMessage `json:"fallback"`
	Logging      *loggingSpec    `json:"logging"`
	BudgetMicros *int64          `json:"budgetMicros"`
	Tests        []testCaseSpec  `json:"tests"`
}

type testCaseSpec struct {
	Name string `json:"name"`
	Ctx  *struct {
		Now  string `json:"now"`
		Unit string `json:"unit"`
	} `json:"ctx"`
	Input       json.RawMessage `json:"input"`
	Expect      json.RawMessage `json:"expect"`
	ExpectError string          `json:"expectError"`
	ExpectPath  []string        `json:"expectPath"`
}

type ruleSpec struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     *bool           `json:"enabled"`
	Priority    int             `json:"priority"`
	When        json.RawMessage `json:"when"`
	Then        json.RawMessage `json:"then"`
	Rollout     *rolloutSpec    `json:"rollout"`
}

type rolloutSpec struct {
	Percent *float64 `json:"percent"`
	Salt    string   `json:"salt"`
}

type tableInputSpec struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

type tableRowSpec struct {
	ID       string            `json:"id"`
	Priority int               `json:"priority"`
	When     map[string]string `json:"when"`
	Then     json.RawMessage   `json:"then"`
}

type fieldSpec struct {
	Target string `json:"target"`
	Expr   string `json:"expr"`
}

type defaultSpec struct {
	Target string          `json:"target"`
	Value  json.RawMessage `json:"value"`
}

type caseSpec struct {
	ID   string `json:"id"`
	When string `json:"when"`
	Next string `json:"next"`
}

type promptSpec struct {
	System string `json:"system"`
	User   string `json:"user"`
}

type nodeSpec struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Description string          `json:"description"`
	From        string          `json:"from"`
	Next        string          `json:"next"`
	OnError     string          `json:"onError"`
	Position    json.RawMessage `json:"position"`

	InputSchema  *schemaRef      `json:"inputSchema"`
	OutputSchema *schemaRef      `json:"outputSchema"`
	Mode         string          `json:"mode"`
	Rules        []ruleSpec      `json:"rules"`
	Default      json.RawMessage `json:"default"`

	HitPolicy string           `json:"hitPolicy"`
	Inputs    []tableInputSpec `json:"inputs"`
	Outputs   []string         `json:"outputs"`
	Rows      []tableRowSpec   `json:"rows"`

	Fields     []fieldSpec   `json:"fields"`
	Expression string        `json:"expression"`
	Defaults   []defaultSpec `json:"defaults"`
	Drop       []string      `json:"drop"`
	OmitNil    *bool         `json:"omitNil"`

	Cases []caseSpec `json:"cases"`

	Lookup     string `json:"lookup"`
	Key        string `json:"key"`
	Gate       string `json:"gate"`
	Experiment string `json:"experiment"`
	Decision   string `json:"decision"`
	Input      string `json:"input"`

	Model           string            `json:"model"`
	TimeoutMs       int               `json:"timeoutMs"`
	Vars            map[string]string `json:"vars"`
	Prompt          *promptSpec       `json:"prompt"`
	ResponseSchema  json.RawMessage   `json:"responseSchema"`
	MaxOutputTokens int               `json:"maxOutputTokens"`
	Cache           json.RawMessage   `json:"cache"`
	Budget          json.RawMessage   `json:"budget"`
}

type segmentSpec struct {
	Type   string `json:"type"`
	Static *struct {
		KeyType     string `json:"keyType"`
		PII         bool   `json:"pii"`
		MemberCount int64  `json:"memberCount"`
	} `json:"static"`
	Dynamic *struct {
		AttributesSchema *schemaRef      `json:"attributesSchema"`
		When             json.RawMessage `json:"when"`
	} `json:"dynamic"`
	Rollout *struct {
		Percent *float64        `json:"percent"`
		Salt    string          `json:"salt"`
		Include json.RawMessage `json:"include"`
	} `json:"rollout"`
}

type lookupSpec struct {
	KeyType     string     `json:"keyType"`
	ValueSchema *schemaRef `json:"valueSchema"`
	RowCount    int64      `json:"rowCount"`
}

type gateRuleSpec struct {
	ID      string          `json:"id"`
	When    json.RawMessage `json:"when"`
	Value   *bool           `json:"value"`
	Rollout *rolloutSpec    `json:"rollout"`
}

type gateSpec struct {
	Enabled          *bool          `json:"enabled"`
	AttributesSchema *schemaRef     `json:"attributesSchema"`
	Rules            []gateRuleSpec `json:"rules"`
	Default          *bool          `json:"default"`
}

type variantSpec struct {
	Name       string          `json:"name"`
	Weight     *float64        `json:"weight"`
	Parameters json.RawMessage `json:"parameters"`
}

type experimentSpec struct {
	Status           string          `json:"status"`
	Hypothesis       string          `json:"hypothesis"`
	Salt             string          `json:"salt"`
	Layer            string          `json:"layer"`
	HoldoutPercent   float64         `json:"holdoutPercent"`
	Targeting        json.RawMessage `json:"targeting"`
	AttributesSchema *schemaRef      `json:"attributesSchema"`
	Variants         []variantSpec   `json:"variants"`
	StoppedVariant   string          `json:"stoppedVariant"`
}

type layerSpec struct {
	Salt        string `json:"salt"`
	Allocations []struct {
		Experiment string  `json:"experiment"`
		Percent    float64 `json:"percent"`
	} `json:"allocations"`
}

// decodeStrict decodes JSON rejecting unknown fields and trailing data.
func decodeStrict(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("spec is empty")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return errors.New("trailing data after JSON value")
	}
	return nil
}

// decodeError turns a JSON decode error into a pointer and message.
func decodeError(base string, err error) (string, string) {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		p := base
		if te.Field != "" {
			p += "/" + strings.ReplaceAll(te.Field, ".", "/")
		}
		return p, fmt.Sprintf("expected %s, got JSON %s", te.Type, te.Value)
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "json: unknown field ") {
		field := strings.Trim(strings.TrimPrefix(msg, "json: unknown field "), `"`)
		return base + "/" + field, "unknown property " + field
	}
	return base, strings.TrimPrefix(msg, "json: ")
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

func ptrEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
