package store

// Fetch7 reads record 7.
func Fetch7(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-7:" + id, nil
}
