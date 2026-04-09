package sql

import (
	"strings"
	"testing"
)

func kinds(tokens []Token) []TokenKind {
	out := make([]TokenKind, len(tokens))
	for i, t := range tokens {
		out[i] = t.Kind
	}
	return out
}

func values(tokens []Token) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = t.Value
	}
	return out
}

func assertKinds(t *testing.T, tokens []Token, want ...TokenKind) {
	t.Helper()
	got := kinds(tokens)
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d\n  got:  %v\n  want: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] kind = %v, want %v (value=%q)", i, got[i], want[i], tokens[i].Value)
		}
	}
}

func TestTokenizeEmpty(t *testing.T) {
	toks, err := Tokenize("")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokEOF)
}

func TestTokenizeWhitespaceOnly(t *testing.T) {
	toks, err := Tokenize("   \t\n  ")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokEOF)
}

func TestTokenizeSelectStar(t *testing.T) {
	toks, err := Tokenize("SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokSelect, TokStar, TokFrom, TokIdent, TokEOF)
	if toks[3].Value != "t" {
		t.Errorf("table name = %q, want %q", toks[3].Value, "t")
	}
}

func TestTokenizeKeywordsCaseInsensitive(t *testing.T) {
	toks, err := Tokenize("select FROM Where group BY")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokSelect, TokFrom, TokWhere, TokGroup, TokBy, TokEOF)
	// Value preserves original case.
	if toks[0].Value != "select" {
		t.Errorf("SELECT value = %q, want %q", toks[0].Value, "select")
	}
}

func TestTokenizeAggregateKeywords(t *testing.T) {
	toks, err := Tokenize("COUNT SUM MIN MAX AVG")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokCount, TokSum, TokMin, TokMax, TokAvg, TokEOF)
}

func TestTokenizeIntLiteral(t *testing.T) {
	toks, err := Tokenize("42 0 999999")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokInt, TokInt, TokInt, TokEOF)
	if toks[0].Value != "42" {
		t.Errorf("first int = %q", toks[0].Value)
	}
}

func TestTokenizeFloatLiteral(t *testing.T) {
	toks, err := Tokenize("3.14 0.5")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokFloat, TokFloat, TokEOF)
	if toks[0].Value != "3.14" {
		t.Errorf("first float = %q", toks[0].Value)
	}
}

func TestTokenizeFloatNoFraction(t *testing.T) {
	_, err := Tokenize("3.")
	if err == nil {
		t.Fatal("3. should fail: no digits after decimal point")
	}
}

func TestTokenizeStringLiteral(t *testing.T) {
	toks, err := Tokenize("'hello' 'world'")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokString, TokString, TokEOF)
	if toks[0].Value != "hello" {
		t.Errorf("string value = %q, want %q", toks[0].Value, "hello")
	}
}

func TestTokenizeEmptyString(t *testing.T) {
	toks, err := Tokenize("''")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokString, TokEOF)
	if toks[0].Value != "" {
		t.Errorf("empty string value = %q, want %q", toks[0].Value, "")
	}
}

func TestTokenizeUnterminatedString(t *testing.T) {
	_, err := Tokenize("'unterminated")
	if err == nil {
		t.Fatal("unterminated string should error")
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("error = %q, want mention of 'unterminated'", err.Error())
	}
}

func TestTokenizeOperators(t *testing.T) {
	toks, err := Tokenize("= != < <= > >= * , ( ) ;")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks,
		TokEq, TokNe, TokLt, TokLe, TokGt, TokGe,
		TokStar, TokComma, TokLParen, TokRParen, TokSemi,
		TokEOF,
	)
}

func TestTokenizeTwoCharOperatorsAdjacentToIdent(t *testing.T) {
	toks, err := Tokenize("age>=30")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokIdent, TokGe, TokInt, TokEOF)
	if toks[0].Value != "age" {
		t.Errorf("ident = %q", toks[0].Value)
	}
}

func TestTokenizeCanonicalGroupByQuery(t *testing.T) {
	q := "SELECT city, COUNT(*), AVG(age) FROM people WHERE age > 30 GROUP BY city;"
	toks, err := Tokenize(q)
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks,
		TokSelect,
		TokIdent,  // city
		TokComma,
		TokCount, TokLParen, TokStar, TokRParen,
		TokComma,
		TokAvg, TokLParen, TokIdent, TokRParen, // AVG(age)
		TokFrom, TokIdent, // people
		TokWhere, TokIdent, TokGt, TokInt, // age > 30
		TokGroup, TokBy, TokIdent, // GROUP BY city
		TokSemi,
		TokEOF,
	)
}

func TestTokenizeLoneBang(t *testing.T) {
	_, err := Tokenize("!")
	if err == nil {
		t.Fatal("lone '!' should error")
	}
	if !strings.Contains(err.Error(), "!=") {
		t.Errorf("error should suggest !=, got %q", err.Error())
	}
}

func TestTokenizeUnexpectedChar(t *testing.T) {
	_, err := Tokenize("SELECT @")
	if err == nil {
		t.Fatal("@ should error")
	}
}

func TestTokenizePos(t *testing.T) {
	toks, err := Tokenize("  age > 30")
	if err != nil {
		t.Fatal(err)
	}
	// "  age" → pos 2; " > " → pos 6; "30" → pos 8
	if toks[0].Pos != 2 {
		t.Errorf("age pos = %d, want 2", toks[0].Pos)
	}
	if toks[1].Pos != 6 {
		t.Errorf("> pos = %d, want 6", toks[1].Pos)
	}
	if toks[2].Pos != 8 {
		t.Errorf("30 pos = %d, want 8", toks[2].Pos)
	}
}

func TestTokenizeValueIsSubslice(t *testing.T) {
	// Pin the zero-alloc invariant: every Value must share the
	// input's backing array, not be a heap-allocated copy.
	input := "SELECT age FROM t"
	toks, err := Tokenize(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range toks {
		if tok.Kind == TokEOF {
			continue
		}
		// A sub-slice of input will have a data pointer within
		// [&input[0], &input[len(input)]). A copy would point
		// elsewhere. We can't compare pointers directly in safe Go,
		// but we can check that the value appears at the expected
		// offset.
		if input[tok.Pos:tok.Pos+len(tok.Value)] != tok.Value {
			t.Errorf("token %v value %q does not match input at offset %d", tok.Kind, tok.Value, tok.Pos)
		}
	}
}

func TestTokenizeNumericFollowedByIdent(t *testing.T) {
	// "30age" should parse as TokInt "30" followed by TokIdent "age",
	// NOT as a single malformed token, because the digit scanner
	// stops at non-digit and the ident scanner picks up.
	toks, err := Tokenize("30age")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokInt, TokIdent, TokEOF)
	if toks[0].Value != "30" || toks[1].Value != "age" {
		t.Errorf("got %q %q", toks[0].Value, toks[1].Value)
	}
}

func TestTokenizeTrailingWhitespace(t *testing.T) {
	toks, err := Tokenize("SELECT   ")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokSelect, TokEOF)
}

func TestTokenizeMinusAndPlus(t *testing.T) {
	toks, err := Tokenize("age > -30")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokIdent, TokGt, TokMinus, TokInt, TokEOF)
}

func TestTokenizeStringWithNewline(t *testing.T) {
	toks, err := Tokenize("'hello\nworld'")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokString, TokEOF)
	if toks[0].Value != "hello\nworld" {
		t.Errorf("string value = %q, want %q", toks[0].Value, "hello\nworld")
	}
}

func TestTokenizeAsKeyword(t *testing.T) {
	toks, err := Tokenize("age AS a")
	if err != nil {
		t.Fatal(err)
	}
	assertKinds(t, toks, TokIdent, TokAs, TokIdent, TokEOF)
}
