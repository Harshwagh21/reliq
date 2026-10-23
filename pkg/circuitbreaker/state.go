package circuitbreaker

import "fmt"

// State is a circuit breaker state.
type State int

const (
	// StateClosed admits calls. Consecutive failures open the circuit.
	StateClosed State = iota
	// StateOpen rejects calls until OpenTimeout elapses.
	StateOpen
	// StateHalfOpen admits one probe call.
	StateHalfOpen
)

// String returns Closed, Open, or HalfOpen.
// Any other value is State(n). State(99) is "State(99)".
func (s State) String() string {
	switch s {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "HalfOpen"
	default:
		return fmt.Sprintf("State(%d)", s)
	}
}
