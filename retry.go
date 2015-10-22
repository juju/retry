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
//	})
// ```
//
// The bare minimum arguments that need to be specified are:
// * Func - the function to call
// * Attempts - the number of times to try Func before giving up
// * Delay - how long to wait between each try that returns an error
//
// Any error that is returned from the `Func` is considered transient.
// In order to identify some errors as fatal, pass in a function for the
// `IsFatalError` CallArgs value.
//
// Exponential backoff is supported by passing a value > 1 for the
// `BackoffFactor` in the CallArgs. This is treated as a multiplier for
// the specified `Delay` each time through the loop. To cap the `Delay`,
// pass a value for the `MaxDelay`.
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

	// BackoffFactor is a multiplier used on the Delay each time the function waits.
	// If not specified, a factor of 1 is used, which means the delay does not increase
	// each time through the loop. A factor of 2 would indicate that the second delay
	// would be twice the first, and the third twice the second, and so on.
	BackoffFactor float64

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
	if args.BackoffFactor == 0 {
		args.BackoffFactor = 1
	}
	if args.Clock == nil {
		args.Clock = clock.WallClock
	}
	if args.Func == nil {
		return errors.NotValidf("missing Func")
	}
	if args.Delay == 0 {
		return errors.NotValidf("missing Delay")
	}
	if args.Attempts == 0 {
		return errors.NotValidf("missing Attempts")
	}
	if args.BackoffFactor < 1 {
		return errors.NotValidf("BackoffFactor of %s", args.BackoffFactor)
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
		// Wait for the delay, and retry
		select {
		case <-args.Clock.After(args.Delay):
		case <-args.Stop:
			return RetryStopped
		}

		args.Delay = ScaleDuration(args.Delay, args.MaxDelay, args.BackoffFactor)
	}
	return errors.Wrap(err, &AttemptsExceeded{err})
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
