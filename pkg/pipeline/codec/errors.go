package codec

import "fmt"

// wrappedError marks panics that an entry-point recoverWrapped converts
// back to a returned error. Anything else escapes the recover.
type wrappedError struct {
	error
}

// recoverWrapped is the deferred body for exported entry points that use
// panic/wrappedError internally. Recovers a wrappedError into *err;
// re-panics anything else.
func recoverWrapped(err *error) {
	if r := recover(); r != nil {
		if we, ok := r.(wrappedError); ok {
			*err = we.error
			return
		}
		panic(r)
	}
}

func panicf(format string, args ...any) {
	panic(wrappedError{error: fmt.Errorf(format, args...)})
}

func wrap(err error) {
	if err != nil {
		panic(wrappedError{err})
	}
}

func assert(msg string, err error) {
	if err != nil {
		panicf("%s: %w", msg, err)
	}
}
