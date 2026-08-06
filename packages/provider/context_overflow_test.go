package provider

import (
	"errors"
	"testing"
)

func TestIsContextOverflowError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "http 413", err: errors.New("provider http 413: request rejected"), want: true},
		{name: "response 413", err: errors.New("provider response 413: request rejected"), want: true},
		{name: "HTTP status code 413", err: errors.New("provider request failed: HTTP status code: 413"), want: true},
		{name: "payload too large", err: errors.New("proxy rejected request: Payload Too Large"), want: true},
		{name: "request entity too large", err: errors.New("gateway: Request Entity Too Large"), want: true},
		{name: "numeric value is not HTTP 413", err: errors.New("provider http 400: max_tokens cannot exceed 41344"), want: false},
		{name: "unqualified numeric value is not HTTP 413", err: errors.New("max_tokens cannot exceed 413"), want: false},
		{name: "context error code", err: errors.New("context_length_exceeded"), want: true},
		{name: "maximum context length", err: errors.New("This model's maximum context length is 128000 tokens"), want: true},
		{name: "context window exceeded", err: errors.New("context window exceeded"), want: true},
		{name: "maximum input token count", err: errors.New("input token count exceeds the maximum number of tokens allowed"), want: true},
		{name: "maximum output token count", err: errors.New("max_tokens exceeds the maximum number of tokens allowed"), want: false},
		{name: "unrelated error", err: errors.New("rate limit exceeded"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflowError(tt.err); got != tt.want {
				t.Fatalf("IsContextOverflowError(%q) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}
