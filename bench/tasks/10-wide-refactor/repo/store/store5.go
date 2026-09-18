package store

// Fetch5 reads record 5.
func Fetch5(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-5:" + id, nil
}
