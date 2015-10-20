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
	return time.After(time.Millisecond)
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
	c.Assert(err, jc.Satisfies, retry.IsRetryCountExceeded)
	c.Assert(clock.delays, gc.HasLen, retry.DefaultRetryCount)
}

func (*retrySuite) TestFailureRetries(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:       func() error { return errors.New("bah") },
		RetryCount: 4,
		Clock:      clock,
	})
	c.Assert(err, jc.Satisfies, retry.IsRetryCountExceeded)
	c.Assert(err, gc.ErrorMatches, `retry count exceeded: bah`)
	c.Assert(clock.delays, gc.DeepEquals, []time.Duration{
		retry.DefaultDelay,
		retry.DefaultDelay,
		retry.DefaultDelay,
		retry.DefaultDelay,
	})
}

func (*retrySuite) TestBackoffFactor(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:          func() error { return errors.New("bah") },
		RetryCount:    4,
		Clock:         clock,
		BackoffFactor: 2,
	})
	c.Assert(err, jc.Satisfies, retry.IsRetryCountExceeded)
	c.Assert(err, gc.ErrorMatches, `retry count exceeded: bah`)
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
		RetryCount: 5,
		Clock:      clock,
		Stop:       stop,
	})
	c.Assert(err, jc.Satisfies, retry.IsRetryStopped)
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
		RetryCount:    3,
		BackoffFactor: 2,
		Clock:         clock,
	})
	c.Assert(err, jc.Satisfies, retry.IsRetryCountExceeded)
	c.Assert(clock.delays, gc.HasLen, 3)
	c.Assert(funcErrors, gc.DeepEquals, []error{funcErr, funcErr, funcErr})
	c.Assert(retries, gc.DeepEquals, []int{1, 2, 3})
}
