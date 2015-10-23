// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The retry package encapsulates the mechanism around retrying commands.
//
// The simple use is to call retry.Call with a function closure.
//
// ```go
//	err := retry.Call(retry.CallArgs{
//		Func:     func() error { ... },
//		Attempts: 5,
//		Delay:    time.Minute,
//		Clock:    clock.WallClock,
//	})
// ```
//
// The bare minimum arguments that need to be specified are:
// * Func - the function to call
// * Attempts - the number of times to try Func before giving up
// * Delay - how long to wait between each try that returns an error
// * Clock - either the wall clock, or some testing clock
//
// Any error that is returned from the `Func` is considered transient.
// In order to identify some errors as fatal, pass in a function for the
// `IsFatalError` CallArgs value.
//
// In order to have the `Delay` change for each iteration, a `BackoffFunc`
// needs to be set on the CallArgs. A simple doubling delay function is
// provided by `DoubleDelay`.
//
// An example of a more complex `BackoffFunc` could be a stepped function such
// as:
//
// ```go
//	func StepDelay(attempt int, last time.Duration) time.Duration {
//		switch attempt{
//		case 1:
//			return time.Second
//		case 2:
//			return 5 * time.Second
//		case 3:
//			return 20 * time.Second
//		case 4:
//			return time.Minute
//		case 5:
//			return 5 * time.Minute
//		default:
//			return 2 * last
//		}
//	}
// ```
//
// Consider some package `foo` that has a `TryAgainError`, which looks something
// like this:
// ```go
//	type TryAgainError struct {
//		After time.Duration
//	}
// ```
// and we create something that looks like this:
//
// ```go
//	type TryAgainHelper struct {
//		next time.Duration
//	}
//
//	func (h *TryAgainHelper) notify(lastError error, attempt int) {
//		if tryAgain, ok := lastError.(*foo.TryAgainError); ok {
//			h.next = tryAgain.After
//		} else {
//			h.next = 0
//		}
//	}
//
//	func (h *TryAgainHelper) next(last time.Duration) time.Duration {
//		if h.next != 0 {
//			return h.next
//		}
//		return last
//	}
// ```
//
// Then we could do this:
// ```go
//	helper := TryAgainHelper{}
//	retry.Call(retry.CallArgs{
//		Func: func() error {
//			return foo.SomeFunc()
//		},
//		NotifyFunc:  helper.notify,
//		BackoffFunc: helper.next,
//		Attempts:    20,
//		Delay:       100 * time.Millisecond,
//		Clock:       clock.WallClock,
//	})
// ```
package retry

import (
	"fmt"
	"math"
	"time"

	"github.com/juju/errors"
	"github.com/juju/utils/clock"
)

const (
	// UnlimitedAttempts can be used as a value for `Attempts` to clearly
	// show to the reader that there is no limit to the number of attempts.
	UnlimitedAttempts = -1
)

var (
	// RetryStopped is the error that is returned from the retry functions
	// when the stop channel has been closed.
	RetryStopped = errors.New("retry stopped")
)

// AttemptsExceeded is the error that is returned when the retry count has
// been hit without the function returning a nil error result. The last error
// returned from the function being retried is available as the LastError
// attribute.
type AttemptsExceeded struct {
	LastError error
}

// Error provides the implementation for the error interface method.
func (e *AttemptsExceeded) Error() string {
	return fmt.Sprintf("attempt count exceeded: %s", e.LastError)
}

// IsAttemptsExceeded returns true if the error is a AttemptsExceeded
// error.
func IsAttemptsExceeded(err error) bool {
	_, ok := err.(*AttemptsExceeded)
	return ok
}

// IsRetryStopped returns true if the error is RetryStopped.
func IsRetryStopped(err error) bool {
	return errors.Cause(err) == RetryStopped
}

// CallArgs is a simple structure used to define the behaviour of the Call
// function.
type CallArgs struct {
	// Func is the function that will be retried if it returns an error result.
	Func func() error

	// IsFatalError is a function that, if set, will be called for every non-
	// nil error result from `Func`. If `IsFatalError` returns true, the error
	// is immediately returned breaking out from any further retries.
	IsFatalError func(error) bool

	// NotifyFunc is a function that is called if Func fails, and the attempt
	// number. The first time this function is called attempt is 1, the second
	// time, attempt is 2 and so on.
	NotifyFunc func(lastError error, attempt int)

	// Attempts specifies the number of times Func should be retried before
	// giving up and returning the `AttemptsExceeded` error. If a negative
	// value is specified, the `Call` will retry forever.
	Attempts int

	// Delay specifies how long to wait between retries.
	Delay time.Duration

	// MaxDelay specifies how longest time to wait between retries. If no
	// value is specified there is no maximum delay.
	MaxDelay time.Duration

	// BackoffFunc allows the caller to provide a function that alters the
	// delay each time through the loop. If this function is not provided the
	// delay is the same each iteration. Alternatively a function such as
	// `retry.DoubleDelay` can be used that will provide an exponential
	// backoff. The first time this function is called attempt is 1, the
	// second time, attempt is 2 and so on.
	BackoffFunc func(attempt int, delay time.Duration) time.Duration

	// Clock defaults to clock.Wall, but allows the caller to pass one in.
	// Primarily used for testing purposes.
	Clock clock.Clock

	// Stop is a channel that can be used to indicate that the waiting should
	// be interrupted. If Stop is nil, then the Call function cannot be interrupted.
	// If the channel is closed prior to the Call function being executed, the
	// Func is still attempted once.
	Stop <-chan struct{}
}

// Validate the values are valid. The ensures that the Func, Delay and Attempts
// have been specified, and that the BackoffFactor makes sense (i.e. one or greater).
// If BackoffFactor is not explicitly set, it is set here to be one.
func (args *CallArgs) Validate() error {
	if args.Func == nil {
		return errors.NotValidf("missing Func")
	}
	if args.Delay == 0 {
		return errors.NotValidf("missing Delay")
	}
	if args.Attempts == 0 {
		return errors.NotValidf("missing Attempts")
	}
	if args.Clock == nil {
		return errors.NotValidf("missing Clock")
	}
	return nil
}

// Call will repeatedly execute the Func until either the function returns no
// error, the retry count is exceeded or the stop channel is closed.
func Call(args CallArgs) error {
	err := args.Validate()
	if err != nil {
		return errors.Trace(err)
	}
	for i := 1; args.Attempts < 0 || i <= args.Attempts; i++ {
		err = args.Func()
		if err == nil {
			return nil
		}
		if args.IsFatalError != nil && args.IsFatalError(err) {
			return errors.Trace(err)
		}
		if args.NotifyFunc != nil {
			args.NotifyFunc(err, i)
		}
		if i == args.Attempts && args.Attempts > 0 {
			break // don't wait before returning the error
		}

		if args.BackoffFunc != nil {
			delay := args.BackoffFunc(i, args.Delay)
			if delay > args.MaxDelay && args.MaxDelay > 0 {
				delay = args.MaxDelay
			}
			args.Delay = delay
		}

		// Wait for the delay, and retry
		select {
		case <-args.Clock.After(args.Delay):
		case <-args.Stop:
			return RetryStopped
		}
	}
	return errors.Wrap(err, &AttemptsExceeded{err})
}

// DoubleDelay provides a simple function that doubles the duration passed in.
// This can then be easily used as the `BackoffFunc` in the `CallArgs`
// structure.
func DoubleDelay(attempt int, delay time.Duration) time.Duration {
	if attempt == 1 {
		return delay
	}
	return delay * 2
}

// ScaleDuration scale up the `current` duration by a factor of `scale`, with
// a capped value of `max`. If `max` is zero, it means there is no maximum
// duration.
func ScaleDuration(current, max time.Duration, scale float64) time.Duration {
	// Any overhead that we may possibly incur by multiplying something by one,
	// is more than overcome by the sleeping that will occur.

	// Since scale may be something like 1.5, or 2 and time.Duration is an
	// int64, we need a little casting here. Also, a negative scale is treated
	// as positive.
	duration := (time.Duration)((float64)(current) * math.Abs(scale))
	if duration > max && max > 0 {
		return max
	}
	return duration
}
