// Command columnar-db is an interactive SQL REPL backed by the
// columnar-db engine. It starts with a preloaded demo dataset and
// lets you run queries against it.
//
// Usage:
//
//	go run ./cmd/columnar-db
//
// Then type SQL:
//
//	columnar-db> SELECT city, COUNT(*), AVG(age) FROM people GROUP BY city ORDER BY city
//	columnar-db> SELECT name, age FROM people WHERE age > 30 ORDER BY age DESC LIMIT 3
//	columnar-db> .schema
//	columnar-db> .quit
package main

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"

	"github.com/iyunbo/columnar-db/exec"
	dbsql "github.com/iyunbo/columnar-db/sql"
	"github.com/iyunbo/columnar-db/storage"
)

func main() {
	rg := loadDemoData()
	schema := rg.Schema()

	fmt.Println("columnar-db — interactive SQL REPL")
	fmt.Printf("Loaded table \"people\": %d rows, %d columns\n", rg.RowCount, len(schema.Names))
	fmt.Println("Type .schema to see columns, .quit to exit, or enter a SQL query.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("columnar-db> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch {
		case line == ".quit" || line == ".exit":
			fmt.Println("Bye.")
			return
		case line == ".schema":
			printSchema(schema)
			continue
		case line == ".help":
			printHelp()
			continue
		}

		runQuery(rg, line)
	}
}

func runQuery(rg *storage.RowGroup, query string) {
	// Parse the query to extract column names for the header.
	tokens, err := dbsql.Tokenize(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	stmt, err := dbsql.Parse(tokens)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	colNames := selectItemNames(stmt)

	op, err := dbsql.Execute(rg, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	headerPrinted := false
	totalRows := 0
	for {
		b, ok := op.Next()
		if !ok {
			break
		}
		if !headerPrinted {
			printHeader(colNames, b)
			headerPrinted = true
		}
		printBatch(b)
		totalRows += b.Len()
	}
	if totalRows == 0 {
		fmt.Println("(0 rows)")
	} else {
		fmt.Printf("(%d rows)\n", totalRows)
	}
	fmt.Println()
}

// selectItemNames extracts readable column names from the SELECT
// list for the REPL header.
func selectItemNames(stmt *dbsql.SelectStmt) []string {
	names := make([]string, len(stmt.Items))
	for i, item := range stmt.Items {
		switch {
		case item.Star:
			names[i] = "*"
		case item.AggFunc != "":
			names[i] = item.AggFunc + "(" + item.AggArg + ")"
		default:
			names[i] = item.Column
		}
	}
	return names
}

func printHeader(names []string, b *exec.Batch) {
	widths := make([]int, len(names))
	for i, name := range names {
		widths[i] = len(name)
		if widths[i] < 6 {
			widths[i] = 6
		}
	}
	for i, name := range names {
		if i > 0 {
			fmt.Print(" | ")
		}
		fmt.Printf("%-*s", widths[i], name)
	}
	fmt.Println()
	for i := range names {
		if i > 0 {
			fmt.Print("-+-")
		}
		fmt.Print(strings.Repeat("-", widths[i]))
	}
	fmt.Println()
}

func printBatch(b *exec.Batch) {
	for _, idx := range b.Sel.Indices() {
		for ci, v := range b.Vectors {
			if ci > 0 {
				fmt.Print(" | ")
			}
			var s string
			if v.IsNull(int(idx)) {
				s = "NULL"
			} else {
				switch v.Type {
				case storage.TypeInt64:
					s = fmt.Sprintf("%d", v.Int64s()[idx])
				case storage.TypeFloat64:
					s = fmt.Sprintf("%.2f", v.Float64s()[idx])
				case storage.TypeString:
					s = v.Strings()[idx]
				case storage.TypeBool:
					s = fmt.Sprintf("%v", v.Bools()[idx])
				}
			}
			fmt.Print(s)
		}
		fmt.Println()
	}
}

func printSchema(schema storage.Schema) {
	fmt.Println("Table: people")
	for i, name := range schema.Names {
		fmt.Printf("  %-12s %s\n", name, schema.Types[i])
	}
	fmt.Println()
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println("  .schema    Show table schema")
	fmt.Println("  .help      Show this help")
	fmt.Println("  .quit      Exit")
	fmt.Println()
	fmt.Println("SQL subset:")
	fmt.Println("  SELECT { * | col [, col]... | agg(col) [, ...] }")
	fmt.Println("  FROM table")
	fmt.Println("  [WHERE col op literal]")
	fmt.Println("  [GROUP BY col [, col]...]")
	fmt.Println("  [ORDER BY col [ASC|DESC]]")
	fmt.Println("  [LIMIT n]")
	fmt.Println()
	fmt.Println("Aggregates: COUNT(*), COUNT(col), SUM, MIN, MAX, AVG")
	fmt.Println()
}

func loadDemoData() *storage.RowGroup {
	rng := rand.New(rand.NewPCG(42, 99))
	n := 1000

	names := []string{
		"Alice", "Bob", "Carol", "Dave", "Eve", "Frank",
		"Grace", "Hank", "Ivy", "Jack", "Karen", "Leo",
		"Mia", "Nick", "Olivia", "Pete", "Quinn", "Rose",
		"Sam", "Tina",
	}
	cities := []string{"Paris", "Lyon", "Kunming", "Shanghai", "Beijing", "Tokyo"}

	nameCol := make([]string, n)
	ageCol := make([]int64, n)
	cityCol := make([]string, n)
	salaryCol := make([]float64, n)

	for i := range n {
		nameCol[i] = names[rng.IntN(len(names))]
		ageCol[i] = int64(20 + rng.IntN(50))
		cityCol[i] = cities[rng.IntN(len(cities))]
		salaryCol[i] = float64(30000+rng.IntN(70000)) + float64(rng.IntN(100))/100
	}

	nc := storage.NewColumnChunkNoNulls("name", storage.NewStringColumnFromSlice(nameCol))
	ac := storage.NewColumnChunkNoNulls("age", storage.NewInt64ColumnFromSlice(ageCol))
	cc := storage.NewColumnChunkNoNulls("city", storage.NewStringColumnFromSlice(cityCol))
	sc := storage.NewColumnChunkNoNulls("salary", storage.NewFloat64ColumnFromSlice(salaryCol))

	rg, err := storage.NewRowGroup(nc, ac, cc, sc)
	if err != nil {
		panic(err)
	}
	return rg
}
