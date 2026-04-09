package sql

import "fmt"

// lookupKeyword does an alloc-free case-insensitive check of val
// against the known keywords. Uses byte-level ASCII lowering
// (val[i] | 0x20) so strings.ToUpper is never called and no
// temporary string is allocated. Returns TokIdent on miss.
func lookupKeyword(val string) TokenKind {
	switch len(val) {
	case 2:
		if asciiEqFold(val, "as") {
			return TokAs
		}
		if asciiEqFold(val, "by") {
			return TokBy
		}
	case 3:
		if asciiEqFold(val, "asc") {
			return TokAsc
		}
		if asciiEqFold(val, "avg") {
			return TokAvg
		}
		if asciiEqFold(val, "max") {
			return TokMax
		}
		if asciiEqFold(val, "min") {
			return TokMin
		}
		if asciiEqFold(val, "sum") {
			return TokSum
		}
	case 4:
		if asciiEqFold(val, "desc") {
			return TokDesc
		}
		if asciiEqFold(val, "from") {
			return TokFrom
		}
	case 5:
		if asciiEqFold(val, "count") {
			return TokCount
		}
		if asciiEqFold(val, "group") {
			return TokGroup
		}
		if asciiEqFold(val, "limit") {
			return TokLimit
		}
		if asciiEqFold(val, "order") {
			return TokOrder
		}
		if asciiEqFold(val, "where") {
			return TokWhere
		}
	case 6:
		if asciiEqFold(val, "select") {
			return TokSelect
		}
	}
	return TokIdent
}

// asciiEqFold returns true if a and lower are equal under ASCII
// case folding. lower MUST be all-lowercase. Only valid for ASCII
// letters — which is all SQL keywords are.
func asciiEqFold(a, lower string) bool {
	if len(a) != len(lower) {
		return false
	}
	for i := range len(a) {
		if a[i]|0x20 != lower[i] {
			return false
		}
	}
	return true
}

// Tokenize consumes the entire input and returns either a complete
// token stream ending in TokEOF, or an error describing the first
// lexical problem found. No partial results.
//
// Token.Value is always a sub-slice of input — never a copy. This
// is the invariant that keeps the lexer allocation-free per token
// (the only allocation is the []Token result slice itself, grown
// via append).
func Tokenize(input string) ([]Token, error) {
	// Pre-allocate for a rough estimate: ~1 token per 5 chars.
	tokens := make([]Token, 0, len(input)/5+1)
	pos := 0

	for pos < len(input) {
		// Skip whitespace.
		if isSpace(input[pos]) {
			pos++
			continue
		}

		start := pos
		ch := input[pos]

		switch {
		// Identifiers and keywords.
		case isLetter(ch):
			pos++
			for pos < len(input) && isIdentChar(input[pos]) {
				pos++
			}
			val := input[start:pos]
			tokens = append(tokens, Token{Kind: lookupKeyword(val), Value: val, Pos: start})

		// Numeric literals (int or float).
		case isDigit(ch):
			pos++
			for pos < len(input) && isDigit(input[pos]) {
				pos++
			}
			kind := TokInt
			if pos < len(input) && input[pos] == '.' {
				// Fractional part.
				pos++
				if pos >= len(input) || !isDigit(input[pos]) {
					return nil, lexErr(start, "expected digit after decimal point")
				}
				for pos < len(input) && isDigit(input[pos]) {
					pos++
				}
				kind = TokFloat
			}
			tokens = append(tokens, Token{Kind: kind, Value: input[start:pos], Pos: start})

		// Single-quoted string literals. No escape sequences for
		// Phase 5; '' (two single quotes) is not supported.
		case ch == '\'':
			pos++ // skip opening quote
			for pos < len(input) && input[pos] != '\'' {
				pos++
			}
			if pos >= len(input) {
				return nil, lexErr(start, "unterminated string literal")
			}
			// Value is the content between quotes (sub-slice of input).
			tokens = append(tokens, Token{Kind: TokString, Value: input[start+1 : pos], Pos: start})
			pos++ // skip closing quote

		// Two-character operators first, then single-character.
		case ch == '!' && pos+1 < len(input) && input[pos+1] == '=':
			tokens = append(tokens, Token{Kind: TokNe, Value: input[start : start+2], Pos: start})
			pos += 2
		case ch == '<' && pos+1 < len(input) && input[pos+1] == '=':
			tokens = append(tokens, Token{Kind: TokLe, Value: input[start : start+2], Pos: start})
			pos += 2
		case ch == '>' && pos+1 < len(input) && input[pos+1] == '=':
			tokens = append(tokens, Token{Kind: TokGe, Value: input[start : start+2], Pos: start})
			pos += 2
		case ch == '<':
			tokens = append(tokens, Token{Kind: TokLt, Value: input[start : start+1], Pos: start})
			pos++
		case ch == '>':
			tokens = append(tokens, Token{Kind: TokGt, Value: input[start : start+1], Pos: start})
			pos++
		case ch == '=':
			tokens = append(tokens, Token{Kind: TokEq, Value: input[start : start+1], Pos: start})
			pos++
		case ch == '*':
			tokens = append(tokens, Token{Kind: TokStar, Value: input[start : start+1], Pos: start})
			pos++
		case ch == '-':
			tokens = append(tokens, Token{Kind: TokMinus, Value: input[start : start+1], Pos: start})
			pos++
		case ch == '+':
			tokens = append(tokens, Token{Kind: TokPlus, Value: input[start : start+1], Pos: start})
			pos++
		case ch == ',':
			tokens = append(tokens, Token{Kind: TokComma, Value: input[start : start+1], Pos: start})
			pos++
		case ch == '(':
			tokens = append(tokens, Token{Kind: TokLParen, Value: input[start : start+1], Pos: start})
			pos++
		case ch == ')':
			tokens = append(tokens, Token{Kind: TokRParen, Value: input[start : start+1], Pos: start})
			pos++
		case ch == ';':
			tokens = append(tokens, Token{Kind: TokSemi, Value: input[start : start+1], Pos: start})
			pos++

		// Lone '!' without '=' is not a valid token.
		case ch == '!':
			return nil, lexErr(start, "unexpected '!' (did you mean '!='?)")

		default:
			return nil, lexErr(start, fmt.Sprintf("unexpected character %q", ch))
		}
	}

	tokens = append(tokens, Token{Kind: TokEOF, Pos: pos})
	return tokens, nil
}

func lexErr(pos int, msg string) error {
	return fmt.Errorf("sql: lexer error at offset %d: %s", pos, msg)
}

func isSpace(c byte) bool  { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isIdentChar(c byte) bool { return isLetter(c) || isDigit(c) }
