package utils

// WriteFunc is a function that implements the io.Writer interface.
type WriteFunc func(p []byte) (n int, err error)

func (w WriteFunc) Write(p []byte) (n int, err error) {
	return w(p)
}
