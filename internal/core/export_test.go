package core

import "reflect"

// TreeSteps returns the start steps the tree of c recorded, in order: on Windows the tree records
// "create_suspended", "assign_job" and "resume" (U22). It reads the steps field of tree through
// reflect, so a build without the field reports no steps instead of failing to compile.
func TreeSteps(c *Child) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	field := reflect.ValueOf(c.tree).FieldByName("steps")
	if !field.IsValid() || field.Kind() != reflect.Slice {
		return nil
	}
	steps := make([]string, field.Len())
	for i := range steps {
		steps[i] = field.Index(i).String()
	}
	return steps
}

// ChildCreationFlags returns the process creation flags c was started with, and whether the
// platform has them (only Windows does).
func ChildCreationFlags(c *Child) (uint32, bool) {
	if c.cmd.SysProcAttr == nil {
		return 0, false
	}
	field := reflect.ValueOf(c.cmd.SysProcAttr).Elem().FieldByName("CreationFlags")
	if !field.IsValid() {
		return 0, false
	}
	return uint32(field.Uint()), true
}
