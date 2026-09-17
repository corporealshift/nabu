package fixture

// Status is where a run ended up.
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// AllStatuses lists every status in display order.
var AllStatuses = []Status{
	StatusPending, StatusRunning, StatusDone, StatusFailed, StatusSkipped,
}
