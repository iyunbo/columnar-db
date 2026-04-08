package sql

import "fmt"

// Lexer is the SQL tokenizer. Step 1 lands this as a stub that
// returns a clear "not implemented" error; Step 2 fills in the
// real scanning logic. Keeping the API shape now means Step 3's
// parser can be sketched against it without blocking on Step 2.
//
// Contract: Tokenize consumes the entire input string and returns
// either a complete slice of Tokens ending in TokEOF, or an error
// describing the first lexical problem it found. No partial
// results.
type Lexer struct {
	// Step 2 will add input, pos, start, tokens scratch here.
}

// Tokenize runs the lexer over the input and returns the full
// token stream. Step 1 stub — Step 2 replaces the body with the
// real scanner.
func Tokenize(input string) ([]Token, error) {
	return nil, fmt.Errorf("sql: Lexer not implemented yet (Phase 5 Step 2); input was %d bytes", len(input))
}
