package repocheck

// isTestOrVersionFile reports whether the version_literal rule skips the file: a test file, or the one
// declared file that owns the format versions of standards (DR-17, STD-6). With no owner declared no
// file is exempt for that reason, so an unset setting fails closed.
func (c *sourceChecker) isTestOrVersionFile() bool {
	return c.isTestFile() || (c.cfg.VersionFile != "" && c.path == c.cfg.VersionFile)
}
