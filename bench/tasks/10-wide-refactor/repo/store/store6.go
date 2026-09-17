package store

// Fetch6 reads record 6.
func Fetch6(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-6:" + id, nil
}
