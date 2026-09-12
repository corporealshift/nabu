package protocol

// Cursor is a client's position in one session's log (spec §4).
type Cursor struct {
	SessionID   string  `json:"session_id"`
	LastEventID *string `json:"last_event_id"`
}

// EventsAfter returns the events after the cursor position, in log order, and
// whether the client will be fully synced once it applies them. A
// last_event_id that is not in the log yields *RPCError nabu_cursor_unknown.
func EventsAfter(log []Event, lastEventID *string) (events []Event, synced bool, err error) {
	if lastEventID == nil {
		return append([]Event(nil), log...), true, nil
	}
	for i, e := range log {
		if e.ID == *lastEventID {
			return append([]Event(nil), log[i+1:]...), true, nil
		}
	}
	return nil, false, NewRPCError(CodeCursorUnknown, "last_event_id "+*lastEventID+" is not in the log")
}

// Synced reports whether a cursor points at the log's last event.
func Synced(log []Event, lastEventID *string) bool {
	if len(log) == 0 {
		return lastEventID == nil
	}
	return lastEventID != nil && *lastEventID == log[len(log)-1].ID
}
