// Package decide asks a decision model typed questions about a state and returns
// typed answers with probabilities (contracts/ai.md "Decisions"). The TypeSafe client
// (Jev) is the calibrated engine; internal/ai adapts its LLM provider to the same
// interface as the fallback.
package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Question types (TypeSafe API "Question types").
const (
	TypeChoice = "choice"
	TypeScore  = "score"
	TypeNoul   = "noul"
)

// Limits documented by TypeSafe: a Choice has at most 255 options, a Score 2..10 levels.
const (
	MaxChoiceOptions = 255
	MaxScoreLevels   = 10
)

// Option is one Choice option and its rubric (nil for none).
type Option struct {
	Key         string
	Description any
}

// Options keep their order on the wire: Jev can lean toward the first option, so the
// order is fixed by the caller rather than by map iteration.
type Options []Option

// MarshalJSON writes the options as a JSON object in order.
func (o Options) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, opt := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(opt.Key)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(opt.Description)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Question is one typed question. Instructions and criteria may be strings or JSON
// structure (TypeSafe "Advanced: structure").
type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// Choice picks one option.
func Choice(instructions any, options Options) Question {
	return Question{Type: TypeChoice, Instructions: instructions, Criteria: options}
}

// Score rates along ordered levels, lowest first.
func Score(instructions any, levels ...any) Question {
	return Question{Type: TypeScore, Instructions: instructions, Criteria: levels}
}

// Noul is a yes/no question; yes and no describe what each answer means ("" to omit).
func Noul(instructions any, yes, no string) Question {
	q := Question{Type: TypeNoul, Instructions: instructions}
	if yes != "" || no != "" {
		q.Criteria = map[string]string{"true": yes, "false": no}
	}
	return q
}

// Validate checks the documented limits.
func (q Question) Validate() error {
	switch q.Type {
	case TypeChoice:
		o, _ := q.Criteria.(Options)
		if len(o) < 2 || len(o) > MaxChoiceOptions {
			return fmt.Errorf("decide: a choice needs 2..%d options", MaxChoiceOptions)
		}
	case TypeScore:
		l, _ := q.Criteria.([]any)
		if len(l) < 2 || len(l) > MaxScoreLevels {
			return fmt.Errorf("decide: a score needs 2..%d levels", MaxScoreLevels)
		}
	case TypeNoul:
	default:
		return fmt.Errorf("decide: unknown question type %q", q.Type)
	}
	return nil
}

// Request asks every question about one state in one call (speculative fan-out).
type Request struct {
	State     any
	Questions map[string]Question
}

// Answer is one typed answer.
type Answer struct {
	Type string `json:"type"`
	// Choice is the most likely option of a Choice.
	Choice string `json:"choice,omitempty"`
	// Score is the probability-weighted level of a Score (0-based, can fall between levels).
	Score float64 `json:"score,omitempty"`
	// Noul is the probability of yes.
	Noul float64 `json:"noul,omitempty"`
	// Probabilities map options (Choice) or level indexes as strings (Score).
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	// Confidence (Choice and Score) is how concentrated the distribution is, 0..1.
	Confidence *float64 `json:"confidence,omitempty"`
}

// Level is the most likely level of a Score (the one confidence is measured from).
func (a Answer) Level() int {
	best, bp := -1, -1.0
	keys := make([]int, 0, len(a.Probabilities))
	for k := range a.Probabilities {
		if i, err := strconv.Atoi(k); err == nil {
			keys = append(keys, i)
		}
	}
	sort.Ints(keys)
	for _, i := range keys {
		if p := a.Probabilities[strconv.Itoa(i)]; p > bp {
			best, bp = i, p
		}
	}
	if best < 0 {
		return int(math.Round(a.Score))
	}
	return best
}

// Probability is the probability of the answer: the chosen option, the most likely
// level, or yes.
func (a Answer) Probability() float64 {
	switch a.Type {
	case TypeChoice:
		return a.Probabilities[a.Choice]
	case TypeScore:
		return a.Probabilities[strconv.Itoa(a.Level())]
	}
	return a.Noul
}

// Conf is the answer's confidence: the model's for Choice and Score, |2p - 1| for a
// Noul (TypeSafe "Confidence"), or nil when the engine reports none.
func (a Answer) Conf() *float64 {
	if a.Type == TypeNoul {
		c := math.Abs(2*a.Noul - 1)
		return &c
	}
	return a.Confidence
}

// Result holds the answers by question id. Token counts are for usage rows only.
type Result struct {
	Model        string
	Answers      map[string]Answer
	InputTokens  int
	OutputTokens int
	// CostUSD is the reported cost of the call (OpenRouter); 0 when unknown.
	CostUSD float64
}

// Decider answers typed questions.
type Decider interface {
	// Name is the engine: "jev" or "llm".
	Name() string
	// Model is the model asked (an alias such as jev-latest).
	Model() string
	// Calibrated reports whether confidences are calibrated (gate on them).
	Calibrated() bool
	Decide(ctx context.Context, req Request) (Result, error)
}

var (
	// ErrUnavailable means the engine failed or timed out after retries.
	ErrUnavailable = errors.New("decision engine unavailable")
	// ErrBusy means the engine kept rate limiting after retries.
	ErrBusy = errors.New("decision engine rate limited")
	// ErrRejected means the engine refused the request (auth or validation).
	ErrRejected = errors.New("decision engine rejected the request")
	// ErrBadOutput means an answer was missing or of the wrong type.
	ErrBadOutput = errors.New("decision engine answer unusable")
	// ErrAuth means the engine refused the key (401, 403). It comes joined with
	// ErrRejected.
	ErrAuth = errors.New("decision engine refused the key")
	// ErrNoFunds means the account behind the key is out of funds (402). It comes joined
	// with ErrUnavailable.
	ErrNoFunds = errors.New("decision engine account out of funds")
)

// Check verifies that res answers every question with the right type and that Choice
// answers name one of the options.
func Check(req Request, res Result) error {
	for id, q := range req.Questions {
		a, ok := res.Answers[id]
		if !ok || a.Type != q.Type {
			return ErrBadOutput
		}
		switch q.Type {
		case TypeChoice:
			o, _ := q.Criteria.(Options)
			found := false
			for _, opt := range o {
				found = found || opt.Key == a.Choice
			}
			if !found {
				return ErrBadOutput
			}
		case TypeNoul:
			if a.Noul < 0 || a.Noul > 1 || math.IsNaN(a.Noul) {
				return ErrBadOutput
			}
		case TypeScore:
			l, _ := q.Criteria.([]any)
			if lv := a.Level(); lv < 0 || lv >= len(l) {
				return ErrBadOutput
			}
		}
	}
	return nil
}
