package exec

// LimitOp caps the number of output rows. It passes through
// child batches until the cumulative row count reaches the limit,
// then narrows the final batch's Selection and signals EOF.
type LimitOp struct {
	child   Operator
	limit   int
	emitted int
}

var _ Operator = (*LimitOp)(nil)

func NewLimitOp(child Operator, limit int) *LimitOp {
	return &LimitOp{child: child, limit: limit}
}

func (l *LimitOp) Next() (*Batch, bool) {
	if l.emitted >= l.limit {
		return nil, false
	}
	b, ok := l.child.Next()
	if !ok {
		return nil, false
	}
	remaining := l.limit - l.emitted
	if b.Len() <= remaining {
		l.emitted += b.Len()
		return b, true
	}
	// Narrow the selection to `remaining` rows.
	indices := b.Sel.Indices()
	b.Sel.Reset()
	for _, idx := range indices[:remaining] {
		b.Sel.AppendUint16Unchecked(idx)
	}
	l.emitted = l.limit
	return b, true
}

func (l *LimitOp) Reset() {
	l.child.Reset()
	l.emitted = 0
}
