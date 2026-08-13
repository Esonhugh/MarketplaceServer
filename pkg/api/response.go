// Package api defines framework-independent JSON response contracts shared by
// MarketplaceServer backend, git, and frontend-facing handlers.
package api

// SuccessResponse wraps a successful JSON payload.
type SuccessResponse[T any] struct {
	Data T `json:"data"`
}

// Success constructs a successful JSON response.
func Success[T any](data T) SuccessResponse[T] {
	return SuccessResponse[T]{Data: data}
}

// ErrorResponse is the stable JSON error shape for HTTP APIs.
type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
	Details   string `json:"details,omitempty"`
}

// NewError constructs an error response with the required stable fields.
func NewError(code, message, requestID string) ErrorResponse {
	return ErrorResponse{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}
}

// NewErrorWithDetails constructs an error response with optional safe text details.
func NewErrorWithDetails(code, message, requestID, details string) ErrorResponse {
	resp := NewError(code, message, requestID)
	resp.Details = details
	return resp
}

// CursorListResponse is the standard cursor-paginated list response.
type CursorListResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// CursorList constructs a cursor-paginated list response.
func CursorList[T any](items []T, nextCursor string) CursorListResponse[T] {
	if items == nil {
		items = []T{}
	}
	return CursorListResponse[T]{
		Items:      items,
		NextCursor: nextCursor,
	}
}

// HealthStatus is the stable status vocabulary for health responses.
type HealthStatus string

const (
	HealthStatusOK       HealthStatus = "ok"
	HealthStatusDegraded HealthStatus = "degraded"
	HealthStatusError    HealthStatus = "error"
)

// HealthResponse is the JSON health-check response.
type HealthResponse struct {
	Status HealthStatus  `json:"status"`
	Checks []HealthCheck `json:"checks,omitempty"`
}

// HealthCheck describes one dependency or subsystem health result.
type HealthCheck struct {
	Name    string       `json:"name"`
	Status  HealthStatus `json:"status"`
	Message string       `json:"message,omitempty"`
}

// NewHealth constructs a health response and omits checks when none are provided.
func NewHealth(status HealthStatus, checks ...HealthCheck) HealthResponse {
	return HealthResponse{
		Status: status,
		Checks: checks,
	}
}
