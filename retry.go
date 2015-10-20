// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package retry

import (
	"fmt"
	"time"

	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/utils/clock"
)

const (
	DefaultDelay      = 100 * time.Millisecond
	DefaultRetryCount = 5
)

var (
	logger = loggo.GetLogger("juju.retry")

	RetryStopped = errors.New("retry stopped")
)

type RetryCountExceeded struct {
	LastError error
}

func (e *RetryCountExceeded) Error() string {
	return fmt.Sprintf("retry count exceeded: %s", e.LastError)
}

func IsRetryCountExceeded(err error) bool {
	err = errors.Cause(err)
	_, ok := err.(*RetryCountExceeded)
	return ok
}

func IsRetryStopped(err error) bool {
	return errors.Cause(err) == RetryStopped
}

type CallArgs struct {
	What          string
	Func          func() error
	RetryCount    int
	Delay         time.Duration
	BackoffFactor float64
	Clock         clock.Clock
	Stop          <-chan struct{}
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

func Call(args CallArgs) error {
	if args.What != "" {
		logger.Tracef("retry %s", args.What)
	}
	args.populateDefaults()
	var err error
	for i := args.RetryCount; i > 0; i-- {
		err = args.Func()
		if err == nil {
			return nil
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
