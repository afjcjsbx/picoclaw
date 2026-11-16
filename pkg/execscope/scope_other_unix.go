//go:build !linux && !darwin && !windows

package execscope

func newPlatformScope(cfg Config) (Scope, error) {
	return newKillPGScope(cfg), nil
}
