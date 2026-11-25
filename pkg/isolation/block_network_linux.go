//go:build linux

package isolation

// insertLinuxUnshareNet splices --unshare-net into bwrap args immediately
// before the -- separator that terminates the bwrap options. Without a network
// namespace the sandbox shares the host loopback, making local services,
// cloud-metadata endpoints (169.254.169.254), and sidecar daemons reachable
// from child processes.
func insertLinuxUnshareNet(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			out := make([]string, 0, len(args)+1)
			out = append(out, args[:i]...)
			out = append(out, "--unshare-net")
			out = append(out, args[i:]...)
			return out
		}
	}
	// Guard: should not happen because buildLinuxBwrapArgs always adds --.
	return append(args, "--unshare-net")
}
