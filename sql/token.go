// Package sql is the SQL frontend: lexer → parser → planner, on
// top of the Phase 2–4 execution operators. Phase 5 lets an end
// user submit a query like
//
//	SELECT city, COUNT(*), AVG(age)
//	FROM people
//	WHERE age > 30
//	GROUP BY city
//
// and have it answered without hand-wiring ScanOp → FilterOp →
// GroupByOp in Go.
//
// Phase 5 scope is deliberately narrow: read-only SELECT over a
// single in-memory RowGroup, one predicate in WHERE, scalar and
// grouped aggregation, no joins, no subqueries, no ORDER BY /
// LIMIT, no DDL / DML. See docs/phase5-steps.md for the full
// grammar and the list of explicit non-goals.
//
// Package layout:
//
//	token.go    Token kinds + Token struct
//	lexer.go    Lexer (input string → []Token)
//	ast.go      Parse tree types
//	parser.go   Hand-rolled recursive-descent parser
//	planner.go  AST → exec operator tree
//	sql.go      Execute() entry point that ties them together
package sql

import "fmt"

// TokenKind identifies the lexical category of a Token. The zero
// value is TokInvalid, which simplifies "have I seen a token yet"
// checks in the parser without needing a separate bool.
type TokenKind uint8

const (
	TokInvalid TokenKind = iota

	// Literals and identifiers
	TokIdent
	TokInt    // e.g. 42
	TokFloat  // e.g. 3.14
	TokString // single-quoted string literal

	// Keywords (case-insensitive at the lexer level)
	TokSelect
	TokFrom
	TokWhere
	TokGroup
	TokBy
	TokAs
	TokCount
	TokSum
	TokMin
	TokMax
	TokAvg

	// Operators
	TokStar  // *
	TokMinus // -
	TokPlus  // +
	TokEq    // =
	TokNe    // !=
	TokLt    // <
	TokLe    // <=
	TokGt    // >
	TokGe    // >=
	TokComma // ,
	TokLParen
	TokRParen
	TokSemi // ;

	// Sentinel
	TokEOF
)

// String returns a short human-readable name for a TokenKind,
// used in parser error messages and test assertions. Parser and
// planner errors prefer String() over the numeric TokenKind so
// error strings stay readable when new kinds are added.
func (k TokenKind) String() string {
	switch k {
	case TokInvalid:
		return "INVALID"
	case TokIdent:
		return "IDENT"
	case TokInt:
		return "INT"
	case TokFloat:
		return "FLOAT"
	case TokString:
		return "STRING"
	case TokSelect:
		return "SELECT"
	case TokFrom:
		return "FROM"
	case TokWhere:
		return "WHERE"
	case TokGroup:
		return "GROUP"
	case TokBy:
		return "BY"
	case TokAs:
		return "AS"
	case TokCount:
		return "COUNT"
	case TokSum:
		return "SUM"
	case TokMin:
		return "MIN"
	case TokMax:
		return "MAX"
	case TokAvg:
		return "AVG"
	case TokStar:
		return "*"
	case TokMinus:
		return "-"
	case TokPlus:
		return "+"
	case TokEq:
		return "="
	case TokNe:
		return "!="
	case TokLt:
		return "<"
	case TokLe:
		return "<="
	case TokGt:
		return ">"
	case TokGe:
		return ">="
	case TokComma:
		return ","
	case TokLParen:
		return "("
	case TokRParen:
		return ")"
	case TokSemi:
		return ";"
	case TokEOF:
		return "EOF"
	}
	return fmt.Sprintf("TokenKind(%d)", k)
}

// Token is one lexical unit produced by the Lexer. Literal values
// are stored in the Value field as the raw source substring;
// numeric parsing happens at the parser level so the lexer stays
// allocation-light.
//
// Pos is the byte offset of the token's first character in the
// original input, used to format error messages ("at offset 42").
// Line/column reporting is a parser-polish follow-up if it turns
// out to be needed.
type Token struct {
	Kind  TokenKind
	Value string
	Pos   int
}
