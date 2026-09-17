package store

// Fetch3 reads record 3.
func Fetch3(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-3:" + id, nil
}
