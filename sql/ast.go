package sql

// AST types for the Phase 5 SQL subset. The grammar is documented
// in docs/phase5-steps.md; this file is the Go representation.
//
// Design choices:
// - No interface hierarchy or visitor pattern. Phase 5 has exactly
//   one statement type (SELECT) and the planner walks the struct
//   directly. Over-engineering the AST would add indirection with
//   no consumer.
// - Fields are exported so the planner (same package) can read
//   them directly without getters.

// SelectStmt is the parse tree for:
//
//	SELECT select_list FROM identifier
//	  [ WHERE predicate ]
//	  [ GROUP BY column_list ]
type SelectStmt struct {
	// Items is the select list — one entry per projected column or
	// aggregate call. A lone "*" is represented as a single
	// SelectItem with Star == true.
	Items []SelectItem

	// FromTable is the identifier after FROM. Phase 5 ignores it
	// (single-RowGroup model); the parser accepts any identifier.
	FromTable string

	// Where is nil when there is no WHERE clause. Step 4 fills in
	// the Predicate parsing; until then the parser rejects WHERE
	// with a clear error.
	Where *Predicate

	// GroupBy lists the column names after GROUP BY (nil if absent).
	GroupBy []string

	// OrderBy is the column to sort by (nil if absent). Phase 5.5
	// scope: single column only.
	OrderBy *OrderByClause

	// Limit is -1 when no LIMIT clause is present, otherwise the
	// row cap.
	Limit int
}

// OrderByClause represents ORDER BY col [ASC|DESC].
type OrderByClause struct {
	Column    string
	Ascending bool // true for ASC (default), false for DESC
}

// SelectItem is one entry in the select list. Exactly one of Star,
// Column, or AggFunc is set (the parser enforces this at parse
// time).
type SelectItem struct {
	Star   bool   // SELECT *
	Column string // SELECT col_name

	// Aggregate call (Step 5). When AggFunc != "", the item is an
	// aggregate: AggFunc("COUNT"/"SUM"/...) over AggArg (column
	// name, or "*" for COUNT(*)).
	AggFunc string
	AggArg  string
}

// Predicate is a simple comparison: column op literal. Step 4
// fills in the parser + planner support.
type Predicate struct {
	Column  string
	Op      TokenKind // TokEq / TokNe / TokLt / TokLe / TokGt / TokGe
	Literal Token     // TokInt / TokFloat / TokString
}
