package sql

import "fmt"

// parser is a hand-rolled recursive-descent parser that consumes
// a []Token (from Tokenize) and produces a *SelectStmt. It is
// private — callers use Parse().
type parser struct {
	tokens []Token
	pos    int
}

// Parse converts a token stream into a SelectStmt.
func Parse(tokens []Token) (*SelectStmt, error) {
	p := &parser{tokens: tokens}
	stmt, err := p.parseSelect()
	if err != nil {
		return nil, err
	}
	// Allow optional trailing semicolon.
	if p.peek().Kind == TokSemi {
		p.advance()
	}
	if p.peek().Kind != TokEOF {
		return nil, p.errorf("unexpected %s after statement", p.peek().Kind)
	}
	return stmt, nil
}

// parseSelect handles:
//
//	SELECT select_list FROM identifier
//	  [ WHERE predicate ]
//	  [ GROUP BY column_list ]
func (p *parser) parseSelect() (*SelectStmt, error) {
	if err := p.expect(TokSelect); err != nil {
		return nil, err
	}

	items, err := p.parseSelectList()
	if err != nil {
		return nil, err
	}

	if err := p.expect(TokFrom); err != nil {
		return nil, err
	}
	table := p.peek()
	if table.Kind != TokIdent {
		return nil, p.errorf("expected table name after FROM, got %s", table.Kind)
	}
	p.advance()

	stmt := &SelectStmt{
		Items:     items,
		FromTable: table.Value,
	}

	// WHERE clause.
	if p.peek().Kind == TokWhere {
		pred, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		stmt.Where = pred
	}

	// GROUP BY (Step 5 fills in).
	if p.peek().Kind == TokGroup {
		return nil, p.errorf("GROUP BY not implemented yet")
	}

	return stmt, nil
}

// parseSelectList handles:
//
//	"*" | select_item ("," select_item)*
func (p *parser) parseSelectList() ([]SelectItem, error) {
	// SELECT *
	if p.peek().Kind == TokStar {
		p.advance()
		return []SelectItem{{Star: true}}, nil
	}

	var items []SelectItem
	for {
		item, err := p.parseSelectItem()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		if p.peek().Kind != TokComma {
			break
		}
		p.advance() // consume ','
	}
	return items, nil
}

// parseSelectItem handles:
//
//	column_ref
//	agg_func "(" ("*" | column_ref) ")"
func (p *parser) parseSelectItem() (SelectItem, error) {
	tok := p.peek()

	// Aggregate function call.
	if tok.Kind.IsAggKeyword() {
		funcName := tok.Value
		p.advance()
		if err := p.expect(TokLParen); err != nil {
			return SelectItem{}, err
		}
		argTok := p.peek()
		var arg string
		if argTok.Kind == TokStar {
			arg = "*"
			p.advance()
		} else if argTok.Kind == TokIdent {
			arg = argTok.Value
			p.advance()
		} else {
			return SelectItem{}, p.errorf("expected column name or * inside %s(), got %s", funcName, argTok.Kind)
		}
		if err := p.expect(TokRParen); err != nil {
			return SelectItem{}, err
		}
		return SelectItem{AggFunc: funcName, AggArg: arg}, nil
	}

	// Plain column reference.
	if tok.Kind != TokIdent {
		return SelectItem{}, p.errorf("expected column name or aggregate function, got %s %q", tok.Kind, tok.Value)
	}
	p.advance()
	return SelectItem{Column: tok.Value}, nil
}

// parsePredicate handles: WHERE column_ref comparator literal
func (p *parser) parsePredicate() (*Predicate, error) {
	p.advance() // consume WHERE

	col := p.peek()
	if col.Kind != TokIdent {
		return nil, p.errorf("expected column name after WHERE, got %s", col.Kind)
	}
	p.advance()

	op := p.peek()
	if !op.Kind.IsComparator() {
		return nil, p.errorf("expected comparator (=, !=, <, <=, >, >=) after column name, got %s", op.Kind)
	}
	p.advance()

	lit := p.peek()
	if !lit.Kind.IsLiteral() {
		return nil, p.errorf("expected literal value after comparator, got %s %q", lit.Kind, lit.Value)
	}
	p.advance()

	return &Predicate{Column: col.Value, Op: op.Kind, Literal: lit}, nil
}


// ---------------------------------------------------------------
// helpers
// ---------------------------------------------------------------

func (p *parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Kind: TokEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() Token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *parser) expect(kind TokenKind) error {
	tok := p.peek()
	if tok.Kind != kind {
		return p.errorf("expected %s, got %s %q", kind, tok.Kind, tok.Value)
	}
	p.advance()
	return nil
}

func (p *parser) errorf(format string, args ...any) error {
	tok := p.peek()
	return fmt.Errorf("sql: parse error at offset %d: %s", tok.Pos, fmt.Sprintf(format, args...))
}
