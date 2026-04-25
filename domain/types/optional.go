package types

// Optional represents a value that may or may not be present.
type Optional[T any] struct {
	value   T
	present bool
}

// Some creates an Optional containing the given value.
func Some[T any](value T) Optional[T] {
	return Optional[T]{value: value, present: true}
}

// None creates an empty Optional.
func None[T any]() Optional[T] {
	return Optional[T]{}
}

// Value returns the value and whether it is present.
func (o Optional[T]) Value() (T, bool) { return o.value, o.present }

// OrElse returns the value if present, otherwise returns the given default.
func (o Optional[T]) OrElse(defaultValue T) T {
	if o.present {
		return o.value
	}
	return defaultValue
}
