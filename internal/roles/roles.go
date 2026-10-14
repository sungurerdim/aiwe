package roles

// Pin is one pinned (source, model, effort) entry of a role (SPEC §8.1).
type Pin struct {
	Source string
	Model  string
	Effort string
}

// Pins returns the role's pinned list. A role with no shipped card returns no entry. Each call
// builds the list again. The skeleton uses one entry for every shipped role until automatic choice
// replaces the list (SPEC §8.1 point 11).
func Pins(id string) []Pin {
	if _, ok := Lookup(id); !ok {
		return nil
	}
	return []Pin{{
		Source: "opencode-go",
		Model:  "deepseek-v4.1-flash",
		Effort: "high",
	}}
}
