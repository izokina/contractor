package codec

// scratch is a buffer that nested parses share. Each user appends past the
// length it found on entry and then takes or truncates back to that length,
// so an inner parse never overwrites an outer one's part.
type scratch[T any] []T

func (s *scratch[T]) take(from int) []T {
	if from == len(*s) {
		return nil
	}
	part := append(make([]T, 0, len(*s)-from), (*s)[from:]...)
	s.truncate(from)
	return part
}

func (s *scratch[T]) truncate(n int) {
	clear((*s)[n:])
	*s = (*s)[:n]
}
