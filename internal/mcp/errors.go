package mcp

import "fmt"

// errMCPInternal creates an error with the err:mcp prefix for internal errors.
func errMCPInternal(msg string) error {
	return fmt.Errorf("err:mcp: %s", msg)
}
