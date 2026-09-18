package store

// Fetch1 reads record 1.
func Fetch1(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-1:" + id, nil
}
