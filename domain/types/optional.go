package types

// Optional represents a value that may or may not be present.
type Optional[T any] struct {
	value   T
	present bool
}

// Of creates an Optional containing the given value.
func Of[T any](value T) Optional[T] {
	return Optional[T]{value: value, present: true}
}

// Empty creates an empty Optional.
func Empty[T any]() Optional[T] {
	return Optional[T]{}
}

// IsPresent returns true if a value is present.
func (o Optional[T]) IsPresent() bool { return o.present }

// Get returns the value and whether it is present.
func (o Optional[T]) Get() (T, bool) { return o.value, o.present }

// OrElse returns the value if present, otherwise returns the given default.
func (o Optional[T]) OrElse(defaultValue T) T {
	if o.present {
		return o.value
	}
	return defaultValue
}
