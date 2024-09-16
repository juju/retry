// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package retry_test

import (
	"math"
	"time"

	"github.com/juju/clock"
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
	now    time.Time
	delays []time.Duration
}

func (mock *mockClock) Now() time.Time {
	return mock.now
}

func (mock *mockClock) After(wait time.Duration) <-chan time.Time {
	mock.delays = append(mock.delays, wait)
	mock.now = mock.now.Add(wait)
	// Note (jam): 2024-09-16 on my machine,
	// 	go test -count 500 -failfast -check.f StopChannel
	// Fails reliably at 1 Microsecond. I think the issue is that at 1us,
	// the clock can tick while the after func is being evaluated.
	return time.After(10 * time.Microsecond)
}

func (*retrySuite) TestSuccessHasNoDelay(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:     func() error { return nil },
		Attempts: 5,
		Delay:    time.Minute,
		Clock:    clock,
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
		Attempts: 5,
		Delay:    time.Minute,
		Clock:    clock,
		Stop:     stop,
	})
	c.Assert(called, jc.IsTrue)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(clock.delays, gc.HasLen, 0)
}

func (*retrySuite) TestAttempts(c *gc.C) {
	clock := &mockClock{}
	funcErr := errors.New("bah")
	err := retry.Call(retry.CallArgs{
		Func:     func() error { return funcErr },
		Attempts: 4,
		Delay:    time.Minute,
		Clock:    clock,
	})
	c.Assert(err, jc.Satisfies, retry.IsAttemptsExceeded)
	// We delay between attempts, and don't delay after the last one.
	c.Assert(clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		time.Minute,
		time.Minute,
	})
}

func (*retrySuite) TestAttemptsExceededError(c *gc.C) {
	clock := &mockClock{}
	funcErr := errors.New("bah")
	err := retry.Call(retry.CallArgs{
		Func:     func() error { return funcErr },
		Attempts: 5,
		Delay:    time.Minute,
		Clock:    clock,
	})
	c.Assert(err, gc.ErrorMatches, `attempt count exceeded: bah`)
	c.Assert(err, jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(retry.LastError(err), gc.Equals, funcErr)
}

func (*retrySuite) TestFatalErrorsNotRetried(c *gc.C) {
	clock := &mockClock{}
	funcErr := errors.New("bah")
	err := retry.Call(retry.CallArgs{
		Func:         func() error { return funcErr },
		IsFatalError: func(error) bool { return true },
		Attempts:     5,
		Delay:        time.Minute,
		Clock:        clock,
	})
	c.Assert(errors.Cause(err), gc.Equals, funcErr)
	c.Assert(clock.delays, gc.HasLen, 0)
}

func (*retrySuite) TestBackoffFactor(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:        func() error { return errors.New("bah") },
		Clock:       clock,
		Attempts:    5,
		Delay:       time.Minute,
		BackoffFunc: retry.DoubleDelay,
	})
	c.Assert(err, jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		time.Minute * 2,
		time.Minute * 4,
		time.Minute * 8,
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
		Attempts: 5,
		Delay:    time.Minute,
		Clock:    clock,
		Stop:     stop,
	})
	c.Assert(err, jc.Satisfies, retry.IsRetryStopped)
	c.Assert(clock.delays, gc.HasLen, 3)
}

func (*retrySuite) TestNotifyFunc(c *gc.C) {
	var (
		clock      = &mockClock{}
		funcErr    = errors.New("bah")
		attempts   []int
		funcErrors []error
	)
	err := retry.Call(retry.CallArgs{
		Func: func() error {
			return funcErr
		},
		NotifyFunc: func(lastError error, attempt int) {
			funcErrors = append(funcErrors, lastError)
			attempts = append(attempts, attempt)
		},
		Attempts: 3,
		Delay:    time.Minute,
		Clock:    clock,
	})
	c.Assert(err, jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(clock.delays, gc.HasLen, 2)
	c.Assert(funcErrors, jc.DeepEquals, []error{funcErr, funcErr, funcErr})
	c.Assert(attempts, jc.DeepEquals, []int{1, 2, 3})
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
		Attempts: retry.UnlimitedAttempts,
		Delay:    time.Minute,
		Clock:    clock,
		Stop:     stop,
	})
	c.Assert(err, jc.Satisfies, retry.IsRetryStopped)
	c.Assert(clock.delays, gc.HasLen, count)
}

func (*retrySuite) TestMaxDuration(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:        func() error { return errors.New("bah") },
		Delay:       time.Minute,
		MaxDuration: 5 * time.Minute,
		Clock:       clock,
	})
	c.Assert(err, jc.Satisfies, retry.IsDurationExceeded)
	c.Assert(clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		time.Minute,
		time.Minute,
		time.Minute,
		time.Minute,
	})
}

func (*retrySuite) TestMaxDurationDoubling(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:        func() error { return errors.New("bah") },
		Delay:       time.Minute,
		MaxDuration: 10 * time.Minute,
		BackoffFunc: retry.DoubleDelay,
		Clock:       clock,
	})
	c.Assert(err, jc.Satisfies, retry.IsDurationExceeded)
	// Stops after seven minutes, because the next wait time
	// would take it to 15 minutes.
	c.Assert(clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
	})
}

func (*retrySuite) TestMaxDelay(c *gc.C) {
	clock := &mockClock{}
	err := retry.Call(retry.CallArgs{
		Func:        func() error { return errors.New("bah") },
		Attempts:    7,
		Delay:       time.Minute,
		MaxDelay:    10 * time.Minute,
		BackoffFunc: retry.DoubleDelay,
		Clock:       clock,
	})
	c.Assert(err, jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(clock.delays, jc.DeepEquals, []time.Duration{
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
		Attempts: 5,
		Delay:    time.Microsecond,
		Clock:    clock.WallClock,
	})
	c.Assert(err, jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(attempts, jc.DeepEquals, []int{1, 2, 3, 4, 5})
}

func (*retrySuite) TestMissingFuncNotValid(c *gc.C) {
	err := retry.Call(retry.CallArgs{
		Attempts: 5,
		Delay:    time.Minute,
		Clock:    clock.WallClock,
	})
	c.Check(err, jc.Satisfies, errors.IsNotValid)
	c.Check(err, gc.ErrorMatches, `missing Func not valid`)
}

func (*retrySuite) TestMissingAttemptsNotValid(c *gc.C) {
	err := retry.Call(retry.CallArgs{
		Func:  func() error { return errors.New("bah") },
		Delay: time.Minute,
		Clock: clock.WallClock,
	})
	c.Check(err, jc.Satisfies, errors.IsNotValid)
	c.Check(err, gc.ErrorMatches, `missing Attempts or MaxDuration not valid`)
}

func (*retrySuite) TestMissingDelayNotValid(c *gc.C) {
	err := retry.Call(retry.CallArgs{
		Func:     func() error { return errors.New("bah") },
		Attempts: 5,
		Clock:    clock.WallClock,
	})
	c.Check(err, jc.Satisfies, errors.IsNotValid)
	c.Check(err, gc.ErrorMatches, `missing Delay not valid`)
}

func (*retrySuite) TestMissingClockNotValid(c *gc.C) {
	err := retry.Call(retry.CallArgs{
		Func:     func() error { return errors.New("bah") },
		Attempts: 5,
		Delay:    time.Minute,
	})
	c.Check(err, jc.Satisfies, errors.IsNotValid)
	c.Check(err, gc.ErrorMatches, `missing Clock not valid`)
}

type expBackoffSuite struct {
	testing.LoggingSuite
}

var _ = gc.Suite(&expBackoffSuite{})

func (*expBackoffSuite) TestExpBackoffWithoutJitter(c *gc.C) {
	backoffFunc := retry.ExpBackoff(200*time.Millisecond, 2*time.Second, 2.0, false)
	expDurations := []time.Duration{
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		1600 * time.Millisecond,
		2000 * time.Millisecond, // capped to maxDelay
	}

	for attempt, expDuration := range expDurations {
		got := backoffFunc(0, attempt)
		c.Assert(got, gc.Equals, expDuration, gc.Commentf("unexpected duration for attempt %d", attempt))
	}
}

func (*expBackoffSuite) TestExpBackoffWithJitter(c *gc.C) {
	minDelay := 200 * time.Millisecond
	backoffFunc := retry.ExpBackoff(minDelay, 2*time.Second, 2.0, true)
	// All of these are allowed to go up to 20% over the expected value
	maxDurations := []time.Duration{
		240 * time.Millisecond,
		480 * time.Millisecond,
		960 * time.Millisecond,
		1920 * time.Millisecond,
		2000 * time.Millisecond, // capped to maxDelay
	}

	for attempt, maxDuration := range maxDurations {
		got := backoffFunc(0, attempt)
		c.Assert(got, jc.GreaterThan, minDelay-1, gc.Commentf("expected jittered duration for attempt %d to be in the [%s, %s] range; got %s", attempt, minDelay, maxDuration, got))
		c.Assert(got, jc.LessThan, maxDuration+1, gc.Commentf("expected jittered duration for attempt %d to be in the [%s, %s] range; got %s", attempt, minDelay, maxDuration, got))
	}
}

// TestExpBackofWithJitterAverage makes sure that turning on Jitter doesn't
// dramatically change the average wait times for sampling. (eg, if we say wait
// 200ms to 2000ms, turning on jitter should keep the wait times roughly aligned with those times).
// This is a little bit tricky, because we expect it to be random, but we'll
// look at the average ratio of the jittered value and the expected backoff value.
func (*expBackoffSuite) TestExpBackofWithJitterAverage(c *gc.C) {
	const (
		// 1.02^100 ~= 10, causing us to go from 200ms to 2s in 100 steps
		minDelay    = 200 * time.Millisecond
		maxDelay    = 2 * time.Second
		maxAttempts = 100
		backoff     = 1.02
	)
	jitterBackoffFunc := retry.ExpBackoff(minDelay, maxDelay, backoff, true)
	noJitterBackoffFunc := retry.ExpBackoff(minDelay, maxDelay, backoff, false)
	ratioSum := 0.0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		jitterValue := jitterBackoffFunc(0, attempt)
		nonJitterValue := noJitterBackoffFunc(0, attempt)
		ratio := float64(jitterValue) / float64(nonJitterValue)
		ratioSum += ratio
		// We have > and < not >=, so we need a bit of flexibility,
		// also float64 imprecision gives us a bit more than 1ns of
		// inaccuracy
		minJitter := time.Duration(0.8*float64(nonJitterValue)) - time.Millisecond
		maxJitter := time.Duration(1.2*float64(nonJitterValue)) + time.Millisecond
		c.Assert(jitterValue, jc.GreaterThan, minJitter,
			gc.Commentf("expected jittered duration for attempt %d to be in the [%s, %s] range; got %s",
				attempt, minJitter, maxJitter, jitterValue))
		c.Assert(jitterValue, jc.LessThan, maxJitter,
			gc.Commentf("expected jittered duration for attempt %d to be in the [%s, %s] range; got %s",
				attempt, minJitter, maxJitter, jitterValue))
	}
	// We could do a geometric mean instead of a arithmetic mean because we
	// are dealing with ratios, but ratios should stay in the range of 0-2,
	// so arithmetic makes sense.
	ratioAvg := ratioSum / maxAttempts
	// In practice, while individual attempts might vary by +/- 20%, they
	// average out over 100 steps, and we actually end up within +/- 1% on
	// average. The most I've seen is a 3.6% variation.
	// We move this out to 10% to avoid an annoying test (eg, string of low
	// randoms).
	c.Check(ratioAvg, jc.GreaterThan, 0.9,
		gc.Commentf("jitter reduced the average duration by %.3f, we expected it to be +/- 2%% on average", ratioAvg))
	c.Check(ratioAvg, jc.LessThan, 1.1,
		gc.Commentf("jitter increased the average duration by %.3f, we expected it to be +/- 2%% on average", ratioAvg))
}

// TestExpBackoffBadExponent says that we'll at least roughly conform to
// expectations even if the user gives us a bad backoff factor.
// We don't have a mechanism for giving an error, but at least hold to min and
// max values.
func (*expBackoffSuite) TestExpBackoffBadExponent(c *gc.C) {
	const (
		// a backoff of 0.8 would put us below 200ms
		minDelay    = 200 * time.Millisecond
		maxDelay    = 2 * time.Second
		maxAttempts = 10
		backoff     = 0.8
	)
	jitterBackoffFunc := retry.ExpBackoff(minDelay, maxDelay, backoff, true)
	noJitterBackoffFunc := retry.ExpBackoff(minDelay, maxDelay, backoff, false)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		jitterValue := jitterBackoffFunc(0, attempt)
		nonJitterValue := noJitterBackoffFunc(0, attempt)
		c.Check(nonJitterValue, jc.GreaterThan, minDelay-1,
			gc.Commentf("expected duration for attempt %d to be in the [%s, %s] range; got %s",
				attempt, minDelay, maxDelay, nonJitterValue))
		c.Check(nonJitterValue, jc.LessThan, maxDelay+1,
			gc.Commentf("expected duration for attempt %d to be in the [%s, %s] range; got %s",
				attempt, minDelay, maxDelay, nonJitterValue))
		// Ensure that even with jitter we are still capped at min and max delay
		minJitter := time.Duration(math.Max(0.8*float64(nonJitterValue), float64(minDelay))) - time.Microsecond
		maxJitter := time.Duration(math.Min(1.2*float64(nonJitterValue), float64(maxDelay))) + time.Microsecond
		c.Check(jitterValue, jc.GreaterThan, minJitter,
			gc.Commentf("expected jittered duration for attempt %d to be in the [%s, %s] range; got %s",
				attempt, minJitter, maxJitter, jitterValue))
		c.Check(jitterValue, jc.LessThan, maxJitter,
			gc.Commentf("expected jittered duration for attempt %d to be in the [%s, %s] range; got %s",
				attempt, minJitter, maxJitter, jitterValue))
	}
}
