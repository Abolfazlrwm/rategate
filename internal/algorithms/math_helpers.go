// Package algorithms
//
// math_helpers.go — small math wrappers used by the approximate sliding
// window. Centralized so the exact path doesn't pay for the `math` import
// in its hot loop graph (the compiler still inlines these).
package algorithms

import "math"

func mathExpImpl(x float64) float64 { return math.Exp(x) }
func mathLnImpl(x float64) float64  { return math.Log(x) }
