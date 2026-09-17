package fixture

// Label is what a status is called in the interface.
var Label = map[Status]string{
	StatusPending: "waiting",
	StatusRunning: "in progress",
	StatusDone:    "finished",
}
