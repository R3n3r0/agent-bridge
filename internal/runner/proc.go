package runner

import "os/exec"

// processController terminates a CLI process together with every process it
// started (for example commands run by its shell tool). Each operating system
// has its own implementation, chosen at build time by newProcessController.
type processController interface {
	// prepare configures cmd before Start, including cmd.Cancel.
	prepare(cmd *exec.Cmd)
	// cleanup kills whatever is left after a cancellation.
	cleanup(cmd *exec.Cmd)
	// stop releases resources after a normal exit.
	stop()
}
