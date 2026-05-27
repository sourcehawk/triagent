package prom

// catalog holds the per-binding metric index. Real fields land in Task 2.
type catalog struct {
	names []string
}

func emptyCatalog() *catalog { return &catalog{} }
