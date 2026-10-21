// Package chain composes policies around one dependency call.
//
// Do and Get apply policies outermost first. They reject a nil context, a nil
// function, or a nil policy, and they return an already-done context's error
// without calling the function.
package chain
