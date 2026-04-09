package sql

import (
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/iyunbo/columnar-db/storage"
)

// Phase 5 Step 6: parity benchmark — SQL-driven vs Phase 4 direct-
// operator numbers. Expected result: SQL adds a one-time plan cost
// (parse + plan) that is amortized across the drain, so steady-state
// ns/op should be within a few percent of direct invocation. If the
// gap is wider, investigate before merging.
//
// Run: go test -bench=SQL -benchmem -run=^$ ./sql/

const benchRows = 1_000_000

var lowCardCities = []string{
	"Paris", "Lyon", "Kunming", "Shanghai", "Beijing",
	"Tokyo", "London", "Berlin", "Madrid", "Rome",
}

var (
	lowCardOnce sync.Once
	lowCardRG   *storage.RowGroup
)

func getLowCardRG(tb testing.TB) *storage.RowGroup {
	lowCardOnce.Do(func() {
		rng := rand.New(rand.NewPCG(1001, 2002))
		cities := make([]string, benchRows)
		ages := make([]int64, benchRows)
		for i := range benchRows {
			cities[i] = lowCardCities[rng.IntN(len(lowCardCities))]
			ages[i] = int64(rng.IntN(90))
		}
		cityCol := storage.NewColumnChunkNoNulls("city", storage.NewStringColumnFromSlice(cities))
		ageCol := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ages))
		rg, err := storage.NewRowGroup(cityCol, ageCol)
		if err != nil {
			tb.Fatal(err)
		}
		lowCardRG = rg
	})
	return lowCardRG
}

const highCardDistinct = 10_000

var (
	highCardOnce sync.Once
	highCardRG   *storage.RowGroup
)

func getHighCardRG(tb testing.TB) *storage.RowGroup {
	highCardOnce.Do(func() {
		rng := rand.New(rand.NewPCG(3003, 4004))
		ids := make([]int64, benchRows)
		ages := make([]int64, benchRows)
		for i := range benchRows {
			ids[i] = int64(rng.IntN(highCardDistinct))
			ages[i] = int64(rng.IntN(90))
		}
		idCol := storage.NewColumnChunkNoNulls("user_id", storage.NewInt64ColumnFromSlice(ids))
		ageCol := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ages))
		rg, err := storage.NewRowGroup(idCol, ageCol)
		if err != nil {
			tb.Fatal(err)
		}
		highCardRG = rg
	})
	return highCardRG
}

// BenchmarkSQLGroupByLowCard measures the full SQL path:
// Execute(rg, query) → Tokenize → Parse → Plan → drain.
// The parse+plan cost is included in the measurement (it runs
// once per iteration, not per batch).
func BenchmarkSQLGroupByLowCard(b *testing.B) {
	rg := getLowCardRG(b)
	query := "SELECT city, COUNT(*), AVG(age) FROM t GROUP BY city"

	var total int64
	b.ReportAllocs()
	for b.Loop() {
		op, err := Execute(rg, query)
		if err != nil {
			b.Fatal(err)
		}
		total = 0
		for {
			batch, ok := op.Next()
			if !ok {
				break
			}
			counts := batch.Vectors[1].Int64s()
			for _, i := range batch.Sel.Indices() {
				total += counts[i]
			}
		}
	}
	if total != benchRows {
		b.Fatalf("total = %d, want %d", total, benchRows)
	}
}

// BenchmarkSQLGroupByLowCardReuse measures the Reset path: parse+plan
// once, then Reset+drain in the loop. This is the steady-state shape
// for a prepared-statement-like usage pattern.
func BenchmarkSQLGroupByLowCardReuse(b *testing.B) {
	rg := getLowCardRG(b)
	op, err := Execute(rg, "SELECT city, COUNT(*), AVG(age) FROM t GROUP BY city")
	if err != nil {
		b.Fatal(err)
	}

	var total int64
	b.ReportAllocs()
	for b.Loop() {
		op.Reset()
		total = 0
		for {
			batch, ok := op.Next()
			if !ok {
				break
			}
			counts := batch.Vectors[1].Int64s()
			for _, i := range batch.Sel.Indices() {
				total += counts[i]
			}
		}
	}
	if total != benchRows {
		b.Fatalf("total = %d, want %d", total, benchRows)
	}
}

// BenchmarkSQLGroupByHighCard — same query, 10k groups.
func BenchmarkSQLGroupByHighCard(b *testing.B) {
	rg := getHighCardRG(b)
	query := "SELECT user_id, COUNT(*), AVG(age) FROM t GROUP BY user_id"

	var total int64
	b.ReportAllocs()
	for b.Loop() {
		op, err := Execute(rg, query)
		if err != nil {
			b.Fatal(err)
		}
		total = 0
		for {
			batch, ok := op.Next()
			if !ok {
				break
			}
			counts := batch.Vectors[1].Int64s()
			for _, i := range batch.Sel.Indices() {
				total += counts[i]
			}
		}
	}
	if total != benchRows {
		b.Fatalf("total = %d, want %d", total, benchRows)
	}
}

// BenchmarkSQLGroupByHighCardReuse — Reset path, 10k groups.
func BenchmarkSQLGroupByHighCardReuse(b *testing.B) {
	rg := getHighCardRG(b)
	op, err := Execute(rg, "SELECT user_id, COUNT(*), AVG(age) FROM t GROUP BY user_id")
	if err != nil {
		b.Fatal(err)
	}

	var total int64
	b.ReportAllocs()
	for b.Loop() {
		op.Reset()
		total = 0
		for {
			batch, ok := op.Next()
			if !ok {
				break
			}
			counts := batch.Vectors[1].Int64s()
			for _, i := range batch.Sel.Indices() {
				total += counts[i]
			}
		}
	}
	if total != benchRows {
		b.Fatalf("total = %d, want %d", total, benchRows)
	}
}
