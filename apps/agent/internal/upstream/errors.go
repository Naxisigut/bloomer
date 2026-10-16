package upstream

// ErrorClass is deliberately independent of untrusted server error messages.
// Only 401 was observed live in P0; other classes are synthetic failure contracts.
type ErrorClass struct {
	Code      string
	Retryable bool
}

func ClassifyStatus(status int) ErrorClass {
	switch {
	case status >= 200 && status < 300:
		return ErrorClass{"ok", false}
	case status == 401 || status == 403:
		return ErrorClass{"authentication_failed", false}
	case status == 429:
		return ErrorClass{"rate_limited", true}
	case status >= 500 && status <= 599:
		return ErrorClass{"upstream_unavailable", true}
	case status >= 300 && status <= 399:
		return ErrorClass{"redirect_rejected", false}
	default:
		return ErrorClass{"upstream_request_rejected", false}
	}
}
