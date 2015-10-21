// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package retry

import (
	"fmt"
	"math"
	"time"

	"github.com/juju/errors"
	"github.com/juju/utils/clock"
)

const (
	// DefaultDelay is the delay used if the caller did not specify one.
	DefaultDelay = 100 * time.Millisecond

	// DefaultRetryAttempts is the number of times that the function is
	// retried if the user did not specify a number for retry attempts.
	DefaultRetryAttempts = 5

	// UnlimitedAttempts can be used as a value for `RetryAttempts` to clearly
	// show to the reader that there is no limit to the number of attempts.
	UnlimitedAttempts = -1
)

var (
	// RetryStopped is the error that is returned from the retry functions
	// when the stop channel has been closed.
	RetryStopped = errors.New("retry stopped")
)

// RetryAttemptsExceeded is the error that is returned when the retry count has
// been hit without the function returning a nil error result. The last error
// returned from the function being retried is available as the LastError
// attribute.
type RetryAttemptsExceeded struct {
	LastError error
}

// Error provides the implementation for the error interface method.
func (e *RetryAttemptsExceeded) Error() string {
	return fmt.Sprintf("retry count exceeded: %s", e.LastError)
}

// IsRetryAttemptsExceeded returns true if the error is a RetryAttemptsExceeded
// error.
func IsRetryAttemptsExceeded(err error) bool {
	_, ok := err.(*RetryAttemptsExceeded)
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

	// RetryAttempts specifies the number of times Func should be retried before
	// giving up and returning the `RetryAttemptsExceeded` error. If this is not
	// specified, the `DefaultRetryAttempts` value is used. If a negative retry
	// count is specified, the `Call` will retry forever.
	RetryAttempts int

	// Delay specifies how long to wait between retries. If no value is specified
	// the `DefaultDelay` value is used.
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

func (args *CallArgs) populateDefaults() {
	if args.BackoffFactor < 1 {
		args.BackoffFactor = 1
	}
	if args.RetryAttempts == 0 {
		args.RetryAttempts = DefaultRetryAttempts
	}
	if args.Delay == 0 {
		args.Delay = DefaultDelay
	}
	if args.Clock == nil {
		args.Clock = clock.WallClock
	}
}

// Call will repeatedly execute the Func until either the function returns no
// error, the retry count is exceeded or the stop channel is closed.
func Call(args CallArgs) error {
	args.populateDefaults()
	var err error
	for i := 0; args.RetryAttempts < 0 || i < args.RetryAttempts; i++ {
		err = args.Func()
		if err == nil {
			return nil
		}
		if args.IsFatalError != nil && args.IsFatalError(err) {
			return errors.Trace(err)
		}
		if i == args.RetryAttempts && args.RetryAttempts > 0 {
			break // don't wait before returning the error
		}
		if args.NotifyFunc != nil {
			args.NotifyFunc(err, i+1)
		}
		// Wait for the delay, and retry
		select {
		case <-args.Clock.After(args.Delay):
		case <-args.Stop:
			return RetryStopped
		}

		args.Delay = ScaleDuration(args.Delay, args.MaxDelay, args.BackoffFactor)
	}
	return errors.Wrap(err, &RetryAttemptsExceeded{err})
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
