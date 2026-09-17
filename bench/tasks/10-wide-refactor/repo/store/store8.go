package store

// Fetch8 reads record 8.
func Fetch8(id string) (string, error) {
	if id == "" {
		return "", ErrMissing
	}
	return "record-8:" + id, nil
}
