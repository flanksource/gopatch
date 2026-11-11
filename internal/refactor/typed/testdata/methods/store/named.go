package store

import "fmt"

// Named implements fmt.Stringer, which lives outside this module.
type Named struct {
	N string
}

// String satisfies fmt.Stringer.
func (n Named) String() string { return n.N }

func asStringer(n Named) fmt.Stringer { return n }
