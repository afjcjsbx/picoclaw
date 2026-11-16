//go:build darwin

package execscope

func newPlatformScope(cfg Config) (Scope, error) {
	return newKillPGScope(cfg), nil
}
