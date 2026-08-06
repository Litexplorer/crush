package acp

import acpsdk "github.com/coder/acp-go-sdk"

// resourceNotFound builds the ACP "Resource not found" error (-32002),
// used when a requested file or directory does not exist.
func resourceNotFound(detail string) *acpsdk.RequestError {
	return &acpsdk.RequestError{
		Code:    -32002,
		Message: "Resource not found",
		Data:    map[string]any{"error": detail},
	}
}
