package system

type System interface {
	CommandRunner
	IsNixOS() bool
	IsRemote() bool
}

// Invoke the `nix-copy-closure` command to copy between two types of
// systems.
func CopyClosures(src System, dest System, paths ...string) error {
	log := src.Logger()

	if len(paths) == 0 {
		log.Debugf("no store paths to copy")
		return nil
	}

	argv := []string{"nix-copy-closure"}

	srcIsRemote := src.IsRemote()
	destIsRemote := dest.IsRemote()

	var commandRunner CommandRunner

	// All type asserts must work here, otherwise the IsRemote() method is
	// implemented incorrectly for a given platform or the conditions are
	// put together incorrectly.
	if srcIsRemote && destIsRemote {
		// remote -> remote, so run this command on the remote source,
		// system, since that is where the paths actually are.
		commandRunner = src
		destAddr := dest.(*SSHSystem).Address()
		argv = append(argv, "--to", destAddr)
	} else if srcIsRemote && !destIsRemote {
		// remote -> local, so use --from and run on the local host (dest), since there
		// is no reliable way to run this on the remote while determining how
		// the local address appears to it.
		commandRunner = dest
		srcAddr := src.(*SSHSystem).Address()
		argv = append(argv, "--from", srcAddr)
	} else if !srcIsRemote && destIsRemote {
		// local -> remote, so run this command on the local host.
		commandRunner = src
		destAddr := dest.(*SSHSystem).Address()
		argv = append(argv, "--to", destAddr)
	} else {
		// local -> local, no-op
		log.Debugf("both systems are local, skipping copy")
		return nil
	}

	argv = append(argv, paths...)

	log.CmdArray(argv)

	cmd := NewCommand(argv[0], argv[1:]...)
	_, err := commandRunner.Run(cmd)
	return err
}
