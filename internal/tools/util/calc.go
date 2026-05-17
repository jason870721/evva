package util

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/johnny1110/evva/internal/tools"
)

const maxFloatDigits = 6

type calcTool struct{}

func (t *calcTool) Name() string { return string(tools.CALC) }

func (t *calcTool) Description() string {
	return "Evaluate a mathematical expression and return the result.\n\n" +
		"Supports: +, -, *, /, ^ (exponentiation), parentheses, and unary +/-.\n" +
		"All calculations use float64; the final result is rounded to 6 decimal places.\n" +
		"Examples: \"((1+2)*3/4)^5\", \"sqrt(2)\" (not yet), \"1 + 2 * 3\""
}

func (t *calcTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["expression"],
		"properties":{
			"expression":{"type":"string","description":"Mathematical expression to evaluate, e.g. ((1+2)*3/4)^5"}
		}
	}`)
}

type calcInput struct {
	Expression string `json:"expression"`
}

func (t *calcTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in calcInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("calc: decode: %v", err)}, nil
	}
	expr := strings.TrimSpace(in.Expression)
	if expr == "" {
		return tools.Result{IsError: true, Content: "calc: expression is required"}, nil
	}

	val, err := evaluate(expr)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("calc: %v", err)}, nil
	}

	return tools.Result{Content: formatResult(val)}, nil
}

// formatResult rounds val to maxFloatDigits decimal places and returns a
// plain string (no scientific notation, trailing zeros stripped after the
// decimal point when appropriate). Uses math/big for exact rounding.
func formatResult(val float64) string {
	if math.IsInf(val, 0) || math.IsNaN(val) {
		return fmt.Sprintf("%g", val)
	}
	bf := new(big.Float).SetPrec(64).SetFloat64(val)
	// Round to maxFloatDigits fractional digits.
	bf.SetMode(big.ToNearestAway)
	s := bf.Text('f', maxFloatDigits)
	// Strip trailing zeros but keep at least one digit after the decimal
	// unless the value is an integer.
	if strings.ContainsRune(s, '.') {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

// ---- recursive-descent parser / evaluator ----

type parser struct {
	s   string
	pos int
}

func newParser(s string) *parser { return &parser{s: s} }

func evaluate(expr string) (float64, error) {
	p := newParser(expr)
	p.skipWhitespace()
	val, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipWhitespace()
	if p.pos < len(p.s) {
		return 0, fmt.Errorf("unexpected trailing input at position %d: %q", p.pos, p.s[p.pos:])
	}
	return val, nil
}

// expr → term (('+' | '-') term)*
func (p *parser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWhitespace()
		if p.pos >= len(p.s) {
			break
		}
		switch p.s[p.pos] {
		case '+':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left += right
		case '-':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left -= right
		default:
			return left, nil
		}
	}
	return left, nil
}

// term → unary (('*' | '/') unary)*
func (p *parser) parseTerm() (float64, error) {
	left, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWhitespace()
		if p.pos >= len(p.s) {
			break
		}
		switch p.s[p.pos] {
		case '*':
			p.pos++
			right, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			left *= right
		case '/':
			p.pos++
			right, err := p.parseUnary()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		default:
			return left, nil
		}
	}
	return left, nil
}

// unary → ('+' | '-') unary | pow
func (p *parser) parseUnary() (float64, error) {
	p.skipWhitespace()
	if p.pos < len(p.s) && p.s[p.pos] == '+' {
		p.pos++
		return p.parseUnary()
	}
	if p.pos < len(p.s) && p.s[p.pos] == '-' {
		p.pos++
		v, err := p.parseUnary()
		return -v, err
	}
	return p.parsePow()
}

// pow → primary ('^' pow)?   (right-associative)
func (p *parser) parsePow() (float64, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return 0, err
	}
	p.skipWhitespace()
	if p.pos < len(p.s) && p.s[p.pos] == '^' {
		p.pos++
		exp, err := p.parseUnary()
		if err != nil {
			return 0, err
		}
		return math.Pow(base, exp), nil
	}
	return base, nil
}

// primary → '(' expr ')' | NUMBER
func (p *parser) parsePrimary() (float64, error) {
	p.skipWhitespace()
	if p.pos >= len(p.s) {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	if p.s[p.pos] == '(' {
		p.pos++
		val, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipWhitespace()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return val, nil
	}
	return p.parseNumber()
}

// parseNumber reads a float literal: optional sign handled by unary,
// so here we just read digits, optional decimal point, optional exponent.
func (p *parser) parseNumber() (float64, error) {
	start := p.pos
	if p.pos < len(p.s) && p.s[p.pos] == '.' {
		p.pos++
		if p.pos >= len(p.s) || !unicode.IsDigit(rune(p.s[p.pos])) {
			return 0, fmt.Errorf("invalid number starting with '.' at position %d", start)
		}
	}
	for p.pos < len(p.s) && unicode.IsDigit(rune(p.s[p.pos])) {
		p.pos++
	}
	if p.pos < len(p.s) && p.s[p.pos] == '.' {
		p.pos++
		for p.pos < len(p.s) && unicode.IsDigit(rune(p.s[p.pos])) {
			p.pos++
		}
	}
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.s) && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
			p.pos++
		}
		if p.pos >= len(p.s) || !unicode.IsDigit(rune(p.s[p.pos])) {
			return 0, fmt.Errorf("invalid exponent at position %d", start)
		}
		for p.pos < len(p.s) && unicode.IsDigit(rune(p.s[p.pos])) {
			p.pos++
		}
	}
	tok := p.s[start:p.pos]
	val, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q at position %d", tok, start)
	}
	return val, nil
}

func (p *parser) skipWhitespace() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t' || p.s[p.pos] == '\n' || p.s[p.pos] == '\r') {
		p.pos++
	}
}
