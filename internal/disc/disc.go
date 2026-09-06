package disc

// Disc is a table-of-contents snapshot. An empty ID means no disc is present.
// A nonempty ID with no Tracks represents a data disc.
type Disc struct {
	ID     string
	Tracks []int
}
