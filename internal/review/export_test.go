package review

// SetContextLimits lowers the context limits, so tests need not render
// 256 MB to reach them.
func (c *Comparer) SetContextLimits(packages int, bytes int64) {
	c.limits = contextLimits{packages: packages, bytes: bytes}
}
