package store

// Fetch2 reads record 2.
func Fetch2(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-2:" + id, nil
}
