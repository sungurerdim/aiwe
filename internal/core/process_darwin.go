package core

// onlyZombies reports false: macOS has no /proc to tell a zombie apart, so a process of the group
// counts as live.
func onlyZombies(int) (bool, error) { return false, nil }
