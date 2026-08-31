package httpassert

// Must returns value when err is nil and panics otherwise. It makes values
// built from static, programmer-controlled input convenient to declare inline.
// Runtime input should handle the constructor error instead.
func Must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
