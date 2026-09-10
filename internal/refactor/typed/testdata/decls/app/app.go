package app

import "strings"

// Unused is referred to by nothing, so it can be deleted.
type Unused struct {
	A int
}

// Used is referred to by Consume, so deleting it must be refused.
type Used struct {
	B int
}

// Consume is the reference that protects Used.
func Consume(u Used) int { return u.B }

// Grouped and Sibling share one type block, so deleting either must leave the
// other and the block's comment behind.
type (
	Grouped struct{}
	Sibling struct{}
)

// orphan is called by nothing.
func orphan() string { return strings.ToUpper("x") }

// recursive refers only to itself, which must not count as a reference.
func recursive(n int) int {
	if n <= 0 {
		return 0
	}
	return recursive(n - 1)
}

// Widget carries a method nothing calls.
type Widget struct{}

// Idle is called by nothing.
func (w Widget) Idle() {}
