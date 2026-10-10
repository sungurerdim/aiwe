// Package core holds the command contract, the result model and the output of every command.
package core

// Status is the status word of a result; it carries the meaning, never a colour.
type Status string

// The status words of SPEC §5.3; each maps to one exit code through the table in outcomes.go.
const (
	StatusOK       Status = "ok"
	StatusError    Status = "error"
	StatusUsage    Status = "usage"
	StatusRed      Status = "red"
	StatusNoChange Status = "no_change"
)

// Outcome is the snake_case name of what a command decided.
type Outcome string

// Result is what every command returns. A red gate or no_change is a Result, not an error.
type Result[T any] struct {
	Status  Status
	Outcome Outcome
	Data    T
	Detail  string
}
