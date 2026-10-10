package core

// Identified is anything with a stable identifier; every port embeds it, so an adapter needs no
// other module for it (SPEC §5.1, §6.1).
type Identified interface {
	ID() string
}
