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

// Of creates an Optional containing the given value.
//
// Deprecated: use Some instead.
func Of[T any](value T) Optional[T] {
	return Some(value)
}

// Empty creates an empty Optional.
//
// Deprecated: use None instead.
func Empty[T any]() Optional[T] {
	return None[T]()
}

// IsPresent returns true if a value is present.
//
// Deprecated: use Value instead.
func (o Optional[T]) IsPresent() bool { return o.present }

// Value returns the value and whether it is present.
func (o Optional[T]) Value() (T, bool) { return o.value, o.present }

// Get returns the value and whether it is present.
//
// Deprecated: use Value instead.
func (o Optional[T]) Get() (T, bool) { return o.Value() }

// OrElse returns the value if present, otherwise returns the given default.
func (o Optional[T]) OrElse(defaultValue T) T {
	if o.present {
		return o.value
	}
	return defaultValue
}
