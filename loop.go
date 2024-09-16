// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package retry

import (
	"time"

	"github.com/juju/errors"
)

// LoopSpec is a simple structure used to define the behaviour of the Loop
// iterator.
type LoopSpec struct {
	// Attempts specifies the number of times Func should be retried before
	// giving up and returning the `AttemptsExceeded` error. If a negative
	// value is specified, the `Call` will retry forever.
	Attempts int

	// Delay specifies how long to wait between retries.
	Delay time.Duration

	// MaxDelay specifies how longest time to wait between retries. If no
	// value is specified there is no maximum delay.
	MaxDelay time.Duration

	// MaxDuration specifies the maximum time the `Call` function should spend
	// iterating over `Func`. The duration is calculated from the start of the
	// `Call` function.  If the next delay time would take the total duration
	// of the call over MaxDuration, then a DurationExceeded error is
	// returned. If no value is specified, Call will continue until the number
	// of attempts is complete.
	MaxDuration time.Duration

	// BackoffFunc allows the caller to provide a function that alters the
	// delay each time through the loop. If this function is not provided the
	// delay is the same each iteration. Alternatively a function such as
	// `retry.DoubleDelay` can be used that will provide an exponential
	// backoff. The first time this function is called attempt is 1, the
	// second time, attempt is 2 and so on.
	BackoffFunc func(delay time.Duration, attempt int) time.Duration

	// Clock provides the mechanism for waiting. Normal program execution is
	// expected to use something like clock.WallClock, and tests can override
	// this to not actually sleep in tests.
	Clock Clock

	// Stop is a channel that can be used to indicate that the waiting should
	// be interrupted. If Stop is nil, then the Call function cannot be interrupted.
	// If the channel is closed prior to the Call function being executed, the
	// Func is still attempted once.
	Stop <-chan struct{}
}

// BackoffFactor returns a new LoopSpec with the backoff function set to
// scale the backoff each time by the factor specified. This is an example
// of the syntactic sugar that could be applied to the spec structures.
func (spec LoopSpec) BackoffFactor(factor int) LoopSpec {
	spec.BackoffFunc = func(delay time.Duration, attempt int) time.Duration {
		if attempt == 1 {
			return delay
		}
		return delay * time.Duration(factor)
	}
	return spec
}

// Validate the values are valid. The ensures that there are enough values
// set in the spec for valid iteration.
func (args *LoopSpec) Validate() error {
	if args.Delay == 0 {
		return errors.NotValidf("missing Delay")
	}
	if args.Clock == nil {
		return errors.NotValidf("missing Clock")
	}
	// One of Attempts or MaxDuration need to be specified
	if args.Attempts == 0 && args.MaxDuration == 0 && args.Stop == nil {
		return errors.NotValidf("missing all of Attempts, MaxDuration or Stop")
	}
	return nil
}

// Loop returns a new loop iterator.
func Loop(spec LoopSpec) *Iterator {
	return &Iterator{spec: spec}
}

// Iterator provides the abstaction around the looping and delays.
type Iterator struct {
	err   error
	count int
	start time.Time
	spec  LoopSpec
}

// Error returns the error from the Next calls. If the spec validate fails,
// that is the error that is returned, otherwise it is one of the loop termination
// errors, timeout, stopped, or attempts exceeded.
func (i *Iterator) Error() error {
	return i.err
}

// Count returns the current iteration if called from within the loop, or the number of
// times the loop was executed if called outside the loop.
func (i *Iterator) Count() int {
	return i.count
}

// Next executes the validation and delay aspects of the loop.
func (i *Iterator) Next(err error) bool {
	if i.count == 0 {
		i.err = i.spec.Validate()
		if i.err == nil {
			i.count++
			i.start = i.spec.Clock.Now()
		}
		return i.err == nil
	}

	// Could theoretically add an IsFatal error test here...
	if err == nil {
		// Loop has finished successfully.
		return false
	}
	if i.spec.Attempts > 0 && i.count >= i.spec.Attempts {
		i.err = errors.Wrap(err, &attemptsExceeded{err})
		return false
	}

	if i.spec.BackoffFunc != nil {
		delay := i.spec.BackoffFunc(i.spec.Delay, i.count)
		if delay > i.spec.MaxDelay && i.spec.MaxDelay > 0 {
			delay = i.spec.MaxDelay
		}
		i.spec.Delay = delay
	}

	elapsedTime := i.spec.Clock.Now().Sub(i.start)
	if i.spec.MaxDuration > 0 && (elapsedTime+i.spec.Delay) > i.spec.MaxDuration {
		i.err = errors.Wrap(err, &durationExceeded{err})
		return false
	}

	// Wait for the delay, and retry
	select {
	case <-i.spec.Clock.After(i.spec.Delay):
	case <-i.spec.Stop:
		i.err = errors.Wrap(err, &retryStopped{err})
		return false
	}
	i.count++
	return true
}
