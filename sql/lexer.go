package sql

import "fmt"

// Tokenize consumes the entire input and returns either a complete
// token stream ending in TokEOF, or an error describing the first
// lexical problem found. No partial results.
//
// Token.Value MUST be a sub-slice of the input string — never a
// copy from a scratch []byte. This is the invariant that lets the
// lexer stay allocation-free per token; Step 2's implementation
// has to preserve it.
func Tokenize(input string) ([]Token, error) {
	return nil, fmt.Errorf("sql: Tokenize not implemented")
}
