// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package retry_test

import (
	"time"

	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/retry"
)

type retrySuite struct {
	testing.LoggingSuite
}

var _ = gc.Suite(&retrySuite{})

type mockClock struct {
	delays []time.Duration
}

func (*mockClock) Now() time.Time {
	return time.Now()
}

func (mock *mockClock) After(wait time.Duration) <-chan time.Time {
	mock.delays = append(mock.delays, wait)
	return time.After(time.Microsecond)
}

func (*retrySuite) TestSuccessHasNoDelay(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:  func() error { return nil },
		Clock: clock,
	})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(clock.delays, gc.HasLen, 0)
}

func (*retrySuite) TestCalledOnceEvenIfStopped(c *gc.C) {
	stop := make(chan struct{})
	clock := &mockClock{}
	called := false
	close(stop)
	err := retry.Call(retry.CallArgs{
		Func: func() error {
			called = true
			return nil
		},
		Clock: clock,
		Stop:  stop,
	})
	c.Assert(called, jc.IsTrue)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(clock.delays, gc.HasLen, 0)
}

func (*retrySuite) TestDefaultRetries(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:  func() error { return errors.New("bah") },
		Clock: clock,
	})
	c.Assert(errors.Cause(err), jc.Satisfies, retry.IsRetryAttemptsExceeded)
	c.Assert(clock.delays, gc.HasLen, retry.DefaultRetryAttempts)
}

func (*retrySuite) TestFailureRetries(c *gc.C) {
	clock := &mockClock{}
	funcErr := errors.New("bah")
	err := retry.Call(retry.CallArgs{
		Func:          func() error { return funcErr },
		RetryAttempts: 4,
		Clock:         clock,
	})
	c.Assert(err, gc.ErrorMatches, `retry count exceeded: bah`)
	cause := errors.Cause(err)
	c.Assert(cause, jc.Satisfies, retry.IsRetryAttemptsExceeded)
	retryError, _ := cause.(*retry.RetryAttemptsExceeded)
	c.Assert(retryError.LastError, gc.Equals, funcErr)
	c.Assert(clock.delays, gc.DeepEquals, []time.Duration{
		retry.DefaultDelay,
		retry.DefaultDelay,
		retry.DefaultDelay,
		retry.DefaultDelay,
	})
}

func (*retrySuite) TestFatalErrorsNotRetried(c *gc.C) {
	clock := &mockClock{}
	funcErr := errors.New("bah")
	err := retry.Call(retry.CallArgs{
		Func:         func() error { return funcErr },
		IsFatalError: func(error) bool { return true },
		Clock:        clock,
	})
	c.Assert(errors.Cause(err), gc.Equals, funcErr)
	c.Assert(clock.delays, gc.HasLen, 0)
}

func (*retrySuite) TestBackoffFactor(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:          func() error { return errors.New("bah") },
		RetryAttempts: 4,
		Clock:         clock,
		BackoffFactor: 2,
	})
	c.Assert(errors.Cause(err), jc.Satisfies, retry.IsRetryAttemptsExceeded)
	c.Assert(clock.delays, gc.DeepEquals, []time.Duration{
		retry.DefaultDelay,
		retry.DefaultDelay * 2,
		retry.DefaultDelay * 4,
		retry.DefaultDelay * 8,
	})
}

func (*retrySuite) TestStopChannel(c *gc.C) {
	clock := &mockClock{}
	stop := make(chan struct{})
	count := 0
	err := retry.Call(retry.CallArgs{
		Func: func() error {
			if count == 2 {
				close(stop)
			}
			count++
			return errors.New("bah")
		},
		RetryAttempts: 5,
		Clock:         clock,
		Stop:          stop,
	})
	c.Assert(errors.Cause(err), jc.Satisfies, retry.IsRetryStopped)
	c.Assert(clock.delays, gc.HasLen, 3)
}

func (*retrySuite) TestNotifyFunc(c *gc.C) {
	var (
		clock      = &mockClock{}
		funcErr    = errors.New("bah")
		retries    []int
		funcErrors []error
	)
	err := retry.Call(retry.CallArgs{
		Func: func() error {
			return funcErr
		},
		NotifyFunc: func(lastError error, retry int) {
			funcErrors = append(funcErrors, lastError)
			retries = append(retries, retry)
		},
		RetryAttempts: 3,
		BackoffFactor: 2,
		Clock:         clock,
	})
	c.Assert(errors.Cause(err), jc.Satisfies, retry.IsRetryAttemptsExceeded)
	c.Assert(clock.delays, gc.HasLen, 3)
	c.Assert(funcErrors, gc.DeepEquals, []error{funcErr, funcErr, funcErr})
	c.Assert(retries, gc.DeepEquals, []int{1, 2, 3})
}

func (*retrySuite) TestInfiniteRetries(c *gc.C) {
	// OK, we can't test infinite, but we'll go for lots.
	clock := &mockClock{}
	stop := make(chan struct{})
	count := 0
	err := retry.Call(retry.CallArgs{
		Func: func() error {
			if count == 111 {
				close(stop)
			}
			count++
			return errors.New("bah")
		},
		NotifyFunc: func(lastError error, attempt int) {
			c.Logf("attempt %d\n", attempt)
		},
		RetryAttempts: retry.UnlimitedAttempts,
		Clock:         clock,
		Stop:          stop,
	})
	c.Assert(errors.Cause(err), jc.Satisfies, retry.IsRetryStopped)
	c.Assert(clock.delays, gc.HasLen, count)
}

func (*retrySuite) TestMaxDelay(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:          func() error { return errors.New("bah") },
		RetryAttempts: 6,
		Delay:         time.Minute,
		MaxDelay:      10 * time.Minute,
		BackoffFactor: 2,
		Clock:         clock,
	})
	c.Assert(errors.Cause(err), jc.Satisfies, retry.IsRetryAttemptsExceeded)
	c.Assert(clock.delays, gc.DeepEquals, []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		10 * time.Minute,
		10 * time.Minute,
	})
}

func (*retrySuite) TestWithWallClock(c *gc.C) {
	var attempts []int
	err := retry.Call(retry.CallArgs{
		Func: func() error { return errors.New("bah") },
		NotifyFunc: func(lastError error, attempt int) {
			attempts = append(attempts, attempt)
		},
		Delay: time.Microsecond,
	})
	c.Assert(errors.Cause(err), jc.Satisfies, retry.IsRetryAttemptsExceeded)
	c.Assert(attempts, jc.DeepEquals, []int{1, 2, 3, 4, 5})
}

func (*retrySuite) TestBackoffNormalisation(c *gc.C) {
	// Backoff values of less than one are set to one.
	for _, factor := range []float64{-2, 0, 0.5} {
		clock := &mockClock{}
		err := retry.Call(retry.CallArgs{
			Func:          func() error { return errors.New("bah") },
			BackoffFactor: factor,
			Clock:         clock,
		})
		c.Check(errors.Cause(err), jc.Satisfies, retry.IsRetryAttemptsExceeded)
		c.Check(clock.delays, gc.DeepEquals, []time.Duration{
			retry.DefaultDelay,
			retry.DefaultDelay,
			retry.DefaultDelay,
			retry.DefaultDelay,
			retry.DefaultDelay,
		})
	}
}

func (*retrySuite) TestScaleDuration(c *gc.C) {
	for i, test := range []struct {
		current time.Duration
		max     time.Duration
		scale   float64
		expect  time.Duration
	}{{
		current: time.Minute,
		scale:   1,
		expect:  time.Minute,
	}, {
		current: time.Minute,
		scale:   2.5,
		expect:  2*time.Minute + 30*time.Second,
	}, {
		current: time.Minute,
		max:     3 * time.Minute,
		scale:   10,
		expect:  3 * time.Minute,
	}, {
		current: time.Minute,
		max:     3 * time.Minute,
		scale:   2,
		expect:  2 * time.Minute,
	}, {
		// scale factors of < 1 are not passed in from the Call function
		// but are supported by ScaleDuration
		current: time.Minute,
		scale:   0.5,
		expect:  30 * time.Second,
	}, {
		current: time.Minute,
		scale:   0,
		expect:  0,
	}, {
		// negative scales are treated as positive
		current: time.Minute,
		scale:   -2,
		expect:  2 * time.Minute,
	}} {
		c.Logf("test %d", i)
		c.Check(retry.ScaleDuration(test.current, test.max, test.scale), gc.Equals, test.expect)
	}
}
