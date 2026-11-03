package runtime

// LocalOperatorFactory constructs explicitly selected metadata services. It never
// opens the core store or starts a daemon/agent as a registration/read side effect.
type LocalOperatorFactory struct{}
