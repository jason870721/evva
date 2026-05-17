package util

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCalc_Evaluate(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "simple addition",
			expr: "1 + 2",
			want: "3",
		},
		{
			name: "simple subtraction",
			expr: "5 - 3",
			want: "2",
		},
		{
			name: "simple multiplication",
			expr: "4 * 3",
			want: "12",
		},
		{
			name: "simple division",
			expr: "10 / 4",
			want: "2.5",
		},
		{
			name: "exponentiation",
			expr: "2 ^ 10",
			want: "1024",
		},
		{
			name: "compound expression",
			expr: "((1+2)*3/4)^5",
			want: "57.665039",
		},
		{
			name: "precedence: multiply before add",
			expr: "1 + 2 * 3",
			want: "7",
		},
		{
			name: "precedence: parentheses override",
			expr: "(1 + 2) * 3",
			want: "9",
		},
		{
			name: "right-associative power",
			expr: "2 ^ 3 ^ 2",
			want: "512", // 2^(3^2) = 2^9
		},
		{
			name: "unary minus",
			expr: "-5 + 3",
			want: "-2",
		},
		{
			name: "unary plus",
			expr: "+5 - 3",
			want: "2",
		},
		{
			name: "double negative",
			expr: "--5",
			want: "5",
		},
		{
			name: "negative exponent",
			expr: "10 ^ -1",
			want: "0.1",
		},
		{
			name: "decimal input",
			expr: "3.14159 * 2",
			want: "6.28318",
		},
		{
			name: "integer division rounds",
			expr: "10 / 3",
			want: "3.333333",
		},
		{
			name: "zero",
			expr: "0 * 999",
			want: "0",
		},
		{
			name: "whitespace tolerance",
			expr: "  1   +   2  ",
			want: "3",
		},
		{
			name: "nested parentheses",
			expr: "(((1 + 2)))",
			want: "3",
		},
		{
			name: "scientific notation input",
			expr: "1e3 + 1",
			want: "1001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluate(tt.expr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			formatted := formatResult(got)
			if formatted != tt.want {
				t.Errorf("%q => %q, want %q", tt.expr, formatted, tt.want)
			}
		})
	}
}

func TestCalc_Errors(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "empty expression",
			expr: "",
			want: "unexpected end",
		},
		{
			name: "division by zero",
			expr: "1 / 0",
			want: "division by zero",
		},
		{
			name: "unclosed paren",
			expr: "(1 + 2",
			want: "missing closing",
		},
		{
			name: "trailing input",
			expr: "1 + 2 3",
			want: "unexpected trailing",
		},
		{
			name: "garbage input",
			expr: "abc",
			want: "invalid number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := evaluate(tt.expr)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestCalc_ToolExecute(t *testing.T) {
	tool := &calcTool{}

	t.Run("happy path", func(t *testing.T) {
		in, _ := json.Marshal(calcInput{Expression: "2 + 3 * 4"})
		res, _ := tool.Execute(context.Background(), in)
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if res.Content != "14" {
			t.Errorf("got %q, want 14", res.Content)
		}
	})

	t.Run("empty expression", func(t *testing.T) {
		in, _ := json.Marshal(calcInput{Expression: ""})
		res, _ := tool.Execute(context.Background(), in)
		if !res.IsError || !strings.Contains(res.Content, "expression is required") {
			t.Errorf("got isErr=%v content=%q", res.IsError, res.Content)
		}
	})

	t.Run("decode error", func(t *testing.T) {
		res, _ := tool.Execute(context.Background(), json.RawMessage(`{bogus`))
		if !res.IsError || !strings.Contains(res.Content, "decode") {
			t.Errorf("got isErr=%v content=%q", res.IsError, res.Content)
		}
	})
}

func TestCalc_Rounding(t *testing.T) {
	tests := []struct {
		val  float64
		want string
	}{
		{1.0, "1"},
		{1.5, "1.5"},
		{1.1234567, "1.123457"}, // round up at 6th decimal
		{1.1234564, "1.123456"}, // round down at 6th decimal
		{0.3333333, "0.333333"},
		{-1.5, "-1.5"},
		{0.0, "0"},
		{100.000001, "100.000001"},
		{100.0000001, "100"},
	}
	for _, tt := range tests {
		got := formatResult(tt.val)
		if got != tt.want {
			t.Errorf("formatResult(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestCalc_InfNaN(t *testing.T) {
	if got := formatResult(math.Inf(1)); got != "+Inf" {
		t.Errorf("Inf -> %q, want +Inf", got)
	}
	if got := formatResult(math.NaN()); got != "NaN" {
		t.Errorf("NaN -> %q, want NaN", got)
	}
}
