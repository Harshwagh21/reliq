// Package chain composes policies around one dependency call.
//
// Do and Get apply policies outermost first. They reject a nil context, then a
// nil function, then a nil policy, and they return an already-done context's
// error without calling the function. On any error, Get returns the zero value.
package chain
