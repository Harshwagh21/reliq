// Package bulkhead limits how many dependency calls run at the same time.
//
// [New] rejects maxConcurrent below 1 and returns an error wrapping
// [chain.ErrInvalidConfig] that names maxConcurrent. A full bulkhead returns
// [chain.ErrBulkheadFull] immediately and does not call the function. The
// permit is released on every exit, including panic and runtime.Goexit. There
// is no queue and no goroutine.
package bulkhead
