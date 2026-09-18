package store

// Fetch4 reads record 4.
func Fetch4(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-4:" + id, nil
}
