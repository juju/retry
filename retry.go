// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package retry

import (
	"fmt"
	"time"

	"github.com/juju/errors"
	"github.com/juju/utils/clock"
)

const (
	// DefaultDelay is the delay used if the caller did not specify one.
	DefaultDelay = 100 * time.Millisecond

	// DefaultRetryCount is the number of times that the function is retried
	// if the user did not specify a retry count.
	DefaultRetryCount = 5
)

var (
	// RetryStopped is the error that is returned from the retry functions
	// when the stop channel has been closed.
	RetryStopped = errors.New("retry stopped")
)

// RetryCountExceeded is the error that is returned when the retry count has
// been hit without the function returning a nil error result. The last error
// returned from the function being retried is available as the LastError
// attribute.
type RetryCountExceeded struct {
	LastError error
}

// Error provides the implementation for the error interface method.
func (e *RetryCountExceeded) Error() string {
	return fmt.Sprintf("retry count exceeded: %s", e.LastError)
}

// IsRetryCountExceeded returns true if the error is a RetryCountExceeded
// error.
func IsRetryCountExceeded(err error) bool {
	err = errors.Cause(err)
	_, ok := err.(*RetryCountExceeded)
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

	// NotifyFunc is a function that is called if Func fails, and the attempt
	// number. The first time this function is called retry is 1, the second time,
	// retry is 2 and so on.
	NotifyFunc func(lastError error, retry int)

	// RetryCount specifies the number of times Func should be retried before
	// giving up and returning the `RetryCountExceeded` error. If this is not
	// specified, the `DefaultRetryCount` value is used.
	RetryCount int

	// Delay specifies how long to wait between retries. If no value is specified
	// the `DefaultDelay` value is used.
	Delay time.Duration

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
	if args.RetryCount < 1 {
		args.RetryCount = DefaultRetryCount
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
	for i := args.RetryCount; i >= 0; i-- {
		err = args.Func()
		if err == nil {
			return nil
		}
		if i == 0 {
			break // don't wait before returning the error
		}
		if args.NotifyFunc != nil {
			args.NotifyFunc(err, args.RetryCount-i+1)
		}
		// Wait for the delay, and retry
		if args.Stop == nil {
			<-args.Clock.After(args.Delay)
		} else {
			select {
			case <-args.Clock.After(args.Delay):
			case <-args.Stop:
				return RetryStopped
			}
		}
		// Since the backoff factory may be something like 1.5, or 2
		// and time.Duration is an int64, we need a little casting here.
		if args.BackoffFactor != 1 {
			args.Delay = (time.Duration)((float64)(args.Delay) * args.BackoffFactor)
		}
	}
	return errors.Wrap(err, &RetryCountExceeded{err})
}
