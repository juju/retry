// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package retry_test

import (
	"time"

	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils/clock"
	gc "gopkg.in/check.v1"

	"github.com/juju/retry"
)

type loopSuite struct {
	testing.LoggingSuite
	clock *mockClock
	spec  retry.LoopSpec
}

var _ = gc.Suite(&loopSuite{})

func (s *loopSuite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.clock = &mockClock{}
	s.spec = retry.LoopSpec{
		Attempts: 5,
		Delay:    time.Minute,
		Clock:    s.clock,
	}
}

func success() error {
	return nil
}

func failure() error {
	return errors.New("bah")
}

type willSucceed struct {
	when  int
	count int
}

func (w *willSucceed) maybe() error {
	w.count++
	if w.count > w.when {
		return nil
	}
	return errors.New("bah")
}

func (s *loopSuite) TestSimpleUsage(c *gc.C) {
	var err error
	what := willSucceed{when: 3}
	for loop := retry.Loop(s.spec); loop.Next(err); {
		err = what.maybe()
	}
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.clock.delays, gc.HasLen, 3)
}

func (s *loopSuite) TestMulitipleLoops(c *gc.C) {
	var err error
	what := willSucceed{when: 3}
	for loop := retry.Loop(s.spec); loop.Next(err); {
		err = what.maybe()
	}
	c.Assert(err, jc.ErrorIsNil)

	what = willSucceed{when: 3}
	for loop := retry.Loop(s.spec); loop.Next(err); {
		err = what.maybe()
	}
	c.Assert(err, jc.ErrorIsNil)

	c.Assert(s.clock.delays, gc.HasLen, 6)
}

func (s *loopSuite) TestSuccessHasNoDelay(c *gc.C) {
	var err error
	called := false
	loop := retry.Loop(s.spec)
	for loop.Next(err) {
		err = success()
		called = true
	}
	c.Assert(loop.Error(), jc.ErrorIsNil)
	c.Assert(loop.Count(), gc.Equals, 1)
	c.Assert(called, jc.IsTrue)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.clock.delays, gc.HasLen, 0)
}

func (s *loopSuite) TestCalledOnceEvenIfStopped(c *gc.C) {
	stop := make(chan struct{})
	called := false
	close(stop)

	s.spec.Stop = stop
	var err error

	loop := retry.Loop(s.spec)
	for loop.Next(err) {
		called = true
		err = success()
	}

	c.Assert(loop.Error(), jc.ErrorIsNil)
	c.Assert(loop.Count(), gc.Equals, 1)
	c.Assert(called, jc.IsTrue)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(s.clock.delays, gc.HasLen, 0)
}

func (s *loopSuite) TestAttempts(c *gc.C) {
	loop := retry.Loop(s.spec)
	var err error
	for loop.Next(err) {
		err = failure()
	}

	c.Assert(err, gc.ErrorMatches, "bah")
	c.Assert(loop.Error(), gc.ErrorMatches, `attempt count exceeded: bah`)
	c.Assert(loop.Error(), jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(retry.LastError(loop.Error()), gc.Equals, err)
	c.Assert(loop.Count(), gc.Equals, 5)
	// We delay between attempts, and don't delay after the last one.
	c.Assert(s.clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		time.Minute,
		time.Minute,
		time.Minute,
	})
}

func (s *loopSuite) TestBackoffFactor(c *gc.C) {
	loop := retry.Loop(s.spec.BackoffFactor(2))
	var err error
	for loop.Next(err) {
		err = failure()
	}

	c.Assert(err, gc.ErrorMatches, "bah")
	c.Assert(loop.Error(), gc.ErrorMatches, `attempt count exceeded: bah`)
	c.Assert(loop.Error(), jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(retry.LastError(loop.Error()), gc.Equals, err)
	c.Assert(loop.Count(), gc.Equals, 5)
	c.Assert(s.clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		time.Minute * 2,
		time.Minute * 4,
		time.Minute * 8,
	})
}

func (s *loopSuite) TestStopChannel(c *gc.C) {
	stop := make(chan struct{})
	s.spec.Stop = stop
	loop := retry.Loop(s.spec)
	var err error
	for loop.Next(err) {
		// Close the stop channel third time through.
		if loop.Count() == 3 {
			close(stop)
		}
		err = failure()
	}

	c.Assert(loop.Error(), jc.Satisfies, retry.IsRetryStopped)
	c.Assert(loop.Count(), gc.Equals, 3)
	c.Assert(err, gc.ErrorMatches, "bah")
	c.Assert(s.clock.delays, gc.HasLen, 3)
}

func (s *loopSuite) TestInfiniteRetries(c *gc.C) {
	// OK, we can't test infinite, but we'll go for lots.
	stop := make(chan struct{})
	s.spec.Attempts = retry.UnlimitedAttempts
	s.spec.Stop = stop

	loop := retry.Loop(s.spec)
	var err error
	for loop.Next(err) {
		// Close the stop channel third time through.
		if loop.Count() == 111 {
			close(stop)
		}
		err = failure()
	}

	c.Assert(loop.Error(), jc.Satisfies, retry.IsRetryStopped)
	c.Assert(s.clock.delays, gc.HasLen, loop.Count())
}

func (s *loopSuite) TestMaxDuration(c *gc.C) {
	spec := retry.LoopSpec{
		Delay:       time.Minute,
		MaxDuration: 5 * time.Minute,
		Clock:       s.clock,
	}
	loop := retry.Loop(spec)
	var err error
	for loop.Next(err) {
		err = failure()
	}
	c.Assert(loop.Error(), jc.Satisfies, retry.IsDurationExceeded)
	c.Assert(err, gc.ErrorMatches, "bah")
	c.Assert(s.clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		time.Minute,
		time.Minute,
		time.Minute,
		time.Minute,
	})
}

func (s *loopSuite) TestMaxDurationDoubling(c *gc.C) {
	spec := retry.LoopSpec{
		Delay:       time.Minute,
		BackoffFunc: retry.DoubleDelay,
		MaxDuration: 10 * time.Minute,
		Clock:       s.clock,
	}
	loop := retry.Loop(spec)
	var err error
	for loop.Next(err) {
		err = failure()
	}

	c.Assert(loop.Error(), jc.Satisfies, retry.IsDurationExceeded)
	c.Assert(err, gc.ErrorMatches, "bah")
	// Stops after seven minutes, because the next wait time
	// would take it to 15 minutes.
	c.Assert(s.clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
	})
}

func (s *loopSuite) TestMaxDelay(c *gc.C) {
	spec := retry.LoopSpec{
		Attempts:    7,
		Delay:       time.Minute,
		MaxDelay:    10 * time.Minute,
		BackoffFunc: retry.DoubleDelay,
		Clock:       s.clock,
	}
	loop := retry.Loop(spec)
	var err error
	for loop.Next(err) {
		err = failure()
	}

	c.Assert(loop.Error(), jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(err, gc.ErrorMatches, "bah")
	c.Assert(s.clock.delays, jc.DeepEquals, []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		10 * time.Minute,
		10 * time.Minute,
	})
}

func (s *loopSuite) TestWithWallClock(c *gc.C) {
	var attempts []int

	s.spec.Clock = clock.WallClock
	s.spec.Delay = time.Millisecond

	loop := retry.Loop(s.spec)
	var err error
	for loop.Next(err) {
		attempts = append(attempts, loop.Count())
		err = failure()
	}

	c.Assert(loop.Error(), jc.Satisfies, retry.IsAttemptsExceeded)
	c.Assert(err, gc.ErrorMatches, "bah")
	c.Assert(attempts, jc.DeepEquals, []int{1, 2, 3, 4, 5})
}

func (s *loopSuite) TestNextCallsValidate(c *gc.C) {
	spec := retry.LoopSpec{
		Delay: time.Minute,
		Clock: s.clock,
	}
	called := false
	loop := retry.Loop(spec)
	for loop.Next(nil) {
		called = true
	}

	c.Assert(called, jc.IsFalse)
	c.Assert(loop.Error(), jc.Satisfies, errors.IsNotValid)
}

func (*loopSuite) TestMissingAttemptsNotValid(c *gc.C) {
	spec := retry.LoopSpec{
		Delay: time.Minute,
		Clock: clock.WallClock,
	}
	err := spec.Validate()
	c.Check(err, jc.Satisfies, errors.IsNotValid)
	c.Check(err, gc.ErrorMatches, `missing all of Attempts, MaxDuration or Stop not valid`)
}

func (*loopSuite) TestMissingDelayNotValid(c *gc.C) {
	spec := retry.LoopSpec{
		Attempts: 5,
		Clock:    clock.WallClock,
	}
	err := spec.Validate()
	c.Check(err, jc.Satisfies, errors.IsNotValid)
	c.Check(err, gc.ErrorMatches, `missing Delay not valid`)
}

func (*loopSuite) TestMissingClockNotValid(c *gc.C) {
	spec := retry.LoopSpec{
		Attempts: 5,
		Delay:    time.Minute,
	}
	err := spec.Validate()
	c.Check(err, jc.Satisfies, errors.IsNotValid)
	c.Check(err, gc.ErrorMatches, `missing Clock not valid`)
}
