//go:build !linux

package tools

func platformShellInvocation(shell shellCommand, command string) shellInvocation {
	return directShellInvocation(shell, command)
}
