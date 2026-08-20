package jobs

import "errors"

var ErrInvalidTransition = errors.New("transición de job no permitida")

// AllowedSources lists the statuses that may move into `to`. Terminal states
// never leave completed/failed/cancelled.
func AllowedSources(to Status) []Status {
	switch to {
	case StatusRunning:
		return []Status{StatusQueued, StatusRetrying}
	case StatusRetrying:
		return []Status{StatusRunning}
	case StatusCompleted:
		return []Status{StatusRunning}
	case StatusFailed:
		return []Status{StatusQueued, StatusRunning, StatusRetrying, StatusWaiting}
	case StatusCancelled:
		return []Status{StatusQueued, StatusRunning, StatusRetrying, StatusWaiting}
	default:
		return nil
	}
}

func CanTransition(from, to Status) bool {
	for _, allowed := range AllowedSources(to) {
		if allowed == from {
			return true
		}
	}
	return false
}

func statusIn(status Status, allowed []Status) bool {
	for _, item := range allowed {
		if item == status {
			return true
		}
	}
	return false
}
